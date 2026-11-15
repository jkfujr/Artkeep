(() => {
  let config, restoring = false, starting = false, polling = false, bypass = false, changedAt = 0, generation = 0
  let active, snapshot, nativeGeneration = 0
  const notified = new Set(), types = new Set()
  const storageKey = () => 'artkeep:ppd:' + (active?.url || location.href.split('#')[0])
  const wait = ms => new Promise(resolve => setTimeout(resolve, ms))
  const emit = (type, data = {}) => window.dispatchEvent(new CustomEvent(type, { detail: { data } }))
  async function message(action, extra = {}) {
    const result = await chrome.runtime.sendMessage({ type: 'ppd', action, requestId: active.requestId, ...extra })
    if (!result?.ok) throw new Error(result?.error || 'Artkeep 扩展暂不可用，请重新加载页面')
    return result.value
  }
  const loading = chrome.runtime.sendMessage({ type: 'config' }).then(result => {
    if (!result?.ok) throw new Error(result?.error || '无法读取 Artkeep 设置')
    config = result.value
    return config
  })
  loading.catch(() => {})

  function show(value) {
    let output = document.getElementById('artkeep-ppd-status')
    if (!output) {
      const button = document.getElementById('startDownload')
      if (!button) return
      output = document.createElement('output'); output.id = 'artkeep-ppd-status'; output.setAttribute('role', 'status')
      output.style.cssText = 'display:block;flex-basis:100%;padding:8px 0;overflow-wrap:anywhere'
      button.parentElement.append(output)
    }
    output.textContent = value
  }
  function remember() { sessionStorage.setItem(storageKey(), JSON.stringify(active)) }
  function clear() { sessionStorage.removeItem(storageKey()); active = null; snapshot = null; notified.clear() }
  function holdNative() {
    // PPD 在 downloadStart 返回后用定时器启动文件；微任务先暂停原队列。
    emit('requestPauseDownload'); emit('downloadPause')
  }
  function resumeNative() { bypass = true; document.getElementById('startDownload')?.click(); bypass = false }
  function requestValue(request) { return new Promise((resolve, reject) => { request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error) }) }
  async function openExisting(name) {
    if (!(await indexedDB.databases()).some(db => db.name === name)) return null
    const request = indexedDB.open(name)
    request.onupgradeneeded = () => request.transaction.abort()
    return requestValue(request)
  }
  async function readSnapshot() {
    const db = await openExisting('PBD')
    if (!db) return null
    try {
      if (!['taskMeta', 'taskData', 'taskStates'].every(name => db.objectStoreNames.contains(name))) throw new Error('PPD 数据格式发生变化，请关闭接管并反馈版本')
      const tx = db.transaction(['taskMeta', 'taskData', 'taskStates'], 'readonly')
      const meta = await requestValue(tx.objectStore('taskMeta').index('url').get(location.href.split('#')[0]))
      if (!meta || meta.id < changedAt || !Number.isInteger(meta.part) || meta.part < 1 || meta.part > 10000) return null
      const requests = Array.from({ length: meta.part }, (_, index) => requestValue(tx.objectStore('taskData').get(Number(String(meta.id) + index))))
      const states = requestValue(tx.objectStore('taskStates').get(meta.id))
      const parts = await Promise.all(requests)
      if (parts.some(part => !Array.isArray(part?.data))) return null
      return { id: meta.id, results: parts.flatMap(part => part.data), states: (await states)?.states || [] }
    } finally { db.close() }
  }
  const signature = value => JSON.stringify(value.results.map(result => [result.id, result.original, result.ext]))
  function nativeNames() {
    let content
    const capture = event => {
      const value = event.detail?.data
      if (value?.title !== '_预览文件名') return
      event.stopImmediatePropagation(); content = value.content
    }
    window.addEventListener('output', capture, true)
    try { emit('previewFileName') } finally { window.removeEventListener('output', capture, true) }
    if (typeof content !== 'string') throw new Error('无法读取 PPD 文件名，请重新加载页面后重试')
    const document = new DOMParser().parseFromString(content, 'text/html'), names = new Map()
    const rows = document.querySelectorAll('p.result')
    if (rows.length) {
      for (const row of rows) {
        const original = row.querySelector('.color999')
        if (!original) continue
        const name = original.textContent; original.remove()
        names.set(name, row.textContent.replace(/^: /, ''))
      }
    } else for (const line of content.split('\n')) {
      const colon = line.indexOf(': ')
      if (colon > 0) names.set(line.slice(0, colon), line.slice(colon + 2))
    }
    return names
  }
  function worksFor(value, ids, names) {
    const works = new Map()
    for (const result of value.results) {
      if (result.type === 3 || !ids.includes(result.id)) continue
      const id = String(result.idNum), work = works.get(id) || { platform: 'pixiv', sourceId: id, meta: result, files: [] }
      const animation = result.type === 2
      const name = names.get(result.original.split('/').pop())
      if (!name) throw new Error('PPD 文件名与抓取结果不一致，请重新加载页面后重试')
      work.files.push({ id: result.id, index: result.index || 0, url: result.original, ext: animation ? 'ugoira' : result.ext, path: animation ? name.replace(/\.[^/.]+$/, '.ugoira') : name })
      works.set(id, work)
    }
    return [...works.values()]
  }
  function bookmarkSettings() {
    const form = document.querySelector('form.settingForm')
    const checked = name => form?.elements.namedItem(name)?.checked === true
    const withTag = form?.elements.namedItem('withTag')?.value || 'origin'
    return { enabled: checked('bmkAfterDL'), withTags: withTag !== 'none', translated: withTag === 'transl', private: form?.elements.namedItem('restrict')?.value === 'yes' }
  }
  async function record(files) {
    const db = await openExisting('DLRecord')
    if (!db) throw new Error('PPD 下载记录尚未就绪，请重新加载页面后重试')
    try {
      const names = [...new Set(files.map(file => 'record' + file.id[0]))]
      if (names.some(name => !db.objectStoreNames.contains(name))) throw new Error('PPD 下载记录格式发生变化，请反馈版本')
      await new Promise((resolve, reject) => {
        const tx = db.transaction(names, 'readwrite')
        tx.oncomplete = resolve; tx.onerror = () => reject(tx.error); tx.onabort = () => reject(tx.error || new Error('PPD 结果已变化'))
        for (const file of files) {
          const result = snapshot.results.find(result => result.id === file.id)
          if (!result) { tx.abort(); return }
          tx.objectStore('record' + file.id[0]).put({ id: file.id, n: file.path, d: result.original.match(/img\/(.*)\//)?.[1] })
        }
      })
    } finally { db.close() }
  }
  async function poll() {
    if (!active || polling) return
    polling = true
    const requestId = active.requestId
    try {
      const status = await message('status')
      if (!active || requestId !== active.requestId || !status.found) return
      let complete = 0
      for (const task of status.tasks) {
        if (task.status !== 'completed') continue
        complete++
        const files = task.files.filter(file => !notified.has(file.id))
        if (!files.length) continue
        await record(files)
        if (!active || requestId !== active.requestId) return
        for (const file of files) {
          notified.add(file.id)
          // 使用原有的队列推进事件；元数据和收藏由 Artkeep 保存。
          if (nativeGeneration === generation) emit('skipDownload', { id: file.id, reason: 'artkeep' })
        }
      }
      const failed = status.tasks.find(task => task.status === 'failed')
      const paused = status.tasks.some(task => task.status === 'paused')
      const cancelled = status.tasks.some(task => task.status === 'cancelled')
      show(failed ? 'Artkeep：' + failed.error + '；点击开始重试' : paused ? 'Artkeep：已暂停' : cancelled ? 'Artkeep：已取消' : `Artkeep：${complete} / ${status.tasks.length}`)
      if (complete === status.tasks.length || cancelled) {
        const novels = snapshot?.results.some(result => result.type === 3) && nativeGeneration === generation
        clear(); show(cancelled ? 'Artkeep：已取消' : 'Artkeep：保存完成')
        if (!cancelled && novels) resumeNative()
      }
    } catch (error) { show('Artkeep：' + error.message) } finally { polling = false }
  }
  async function start() {
    if (starting) return
    starting = true
    try {
      await loading
      if (!config.takeover && !active) { resumeNative(); return }
      show('Artkeep：正在读取抓取结果')
      let value
      for (let i = 0; i < 40 && !value; i++) { value = await readSnapshot(); if (!value) await wait(200) }
      if (!value) throw new Error('PPD 抓取结果尚未保存，请稍后点击开始重试')
      const key = signature(value)
      if (active && active.signature !== key) throw new Error('抓取结果已变化，请先取消原 Artkeep 任务')
      if (!active) {
        const ids = value.results.filter((result, index) => result.type !== 3 && value.states[index] !== 1).map(result => result.id)
        if (!ids.length) { resumeNative(); return }
        active = { requestId: 'ppd-' + value.id, signature: key, ids, url: location.href.split('#')[0] }; remember()
      }
      snapshot = value; nativeGeneration = generation; notified.clear()
      const status = await message('status')
      if (!status.found) await message('submit', { works: worksFor(value, active.ids, nativeNames()), bookmark: bookmarkSettings() })
      await message('resume')
      await poll()
    } catch (error) { show('Artkeep：' + error.message) } finally { starting = false }
  }

  for (const type of ['crawlStart', 'crawlComplete', 'resultChange', 'resume']) window.addEventListener(type, () => {
    generation++; changedAt = type === 'resume' ? 0 : Date.now()
    if (type === 'crawlStart' || type === 'resume') types.clear()
  }, true)
  window.addEventListener('addResult', event => { const type = event.detail?.data?.type; if (Number.isInteger(type)) types.add(type) }, true)
  window.addEventListener('readyDownload', () => {
    if (active && !snapshot) queueMicrotask(() => { holdNative(); start() })
  }, true)
  window.addEventListener('downloadStart', () => {
    if (bypass || config && !config.takeover && !active || types.size === 1 && types.has(3)) return
    queueMicrotask(() => { holdNative(); start() })
  }, true)
  window.addEventListener('click', event => {
    if (!active) return
    const button = event.target.closest?.('#pauseDownload, #stopDownload')
    if (!button) return
    event.preventDefault(); event.stopImmediatePropagation()
    const action = button.id === 'pauseDownload' ? 'pause' : 'cancel'
    if (action === 'cancel' && !window.confirm('放弃当前 Artkeep 收藏任务？')) return
    message(action).then(() => { if (action === 'cancel') { clear(); emit('downloadStop') }; show(action === 'pause' ? 'Artkeep：已暂停' : 'Artkeep：已取消') }).catch(error => show('Artkeep：' + error.message))
  }, true)
  function scan() {
    if (!document.querySelector('form.settingForm') || !document.getElementById('startDownload')) return
    if (document.documentElement.dataset.artkeepPpd !== 'ready') document.documentElement.dataset.artkeepPpd = 'ready'
    if (!restoring) {
      restoring = true
      try { active = JSON.parse(sessionStorage.getItem(storageKey()) || 'null') } catch { active = null }
    }
  }
  new MutationObserver(scan).observe(document.documentElement, { childList: true, subtree: true })
  scan()
  setInterval(() => {
    chrome.runtime.sendMessage({ type: 'config' }).then(result => { if (result?.ok) config = result.value }).catch(() => {})
    if (snapshot) poll()
  }, 1500)
})()
