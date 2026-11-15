import { read, saveBatch, updateTask } from './db.js'
import { settings, mediaURL, extractTweet } from './shared.js'

let creating
async function runner() {
  if (!(await chrome.runtime.getContexts({ contextTypes: ['OFFSCREEN_DOCUMENT'] })).length) {
    await (creating ||= chrome.offscreen.createDocument({ url: 'offscreen.html', reasons: ['BLOBS', 'DOM_PARSER'], justification: '保存收藏媒体、生成附属文件并续传到 Artkeep' }).finally(() => { creating = null }))
  }
  await chrome.runtime.sendMessage({ target: 'engine', type: 'run' })
}

async function pixiv(tabId, id) {
  const [result] = await chrome.scripting.executeScript({ target: { tabId }, world: 'MAIN', func: async id => {
    const get = async path => { const r = await fetch(path, { credentials: 'same-origin' }); if (!r.ok) throw new Error(`Pixiv 返回 ${r.status}`); const v = await r.json(); if (v.error) throw new Error(v.message); return v.body }
    const body = await get(`/ajax/illust/${id}`)
    const media = body.illustType === 2 ? await get(`/ajax/illust/${id}/ugoira_meta`) : await get(`/ajax/illust/${id}/pages`)
    return { body, media }
  }, args: [id] })
  if (!result?.result) throw new Error('请重新打开 Pixiv 作品页面')
  const { body: b, media } = result.result
  const meta = { aiType: b.aiType, idNum: String(b.illustId), id: `${b.illustId}_p0`, title: b.illustTitle, description: b.illustComment, pageCount: b.pageCount, tags: b.tags.tags.map(t => t.tag), user: b.userName, userId: String(b.userId), fullWidth: b.width, fullHeight: b.height, date: b.createDate, uploadDate: b.uploadDate, type: b.illustType, xRestrict: b.xRestrict, thumb: b.urls.small, original: b.urls.original, bmk: b.bookmarkCount, ugoiraInfo: b.illustType === 2 ? media : null }
  const files = b.illustType === 2 ? [{ url: media.originalSrc || media.src, ext: 'ugoira', id: String(b.illustId), index: 0 }] : media.map((p, index) => ({ url: p.urls.original, ext: new URL(p.urls.original).pathname.split('.').pop(), id: `${b.illustId}_p${index}`, index }))
  return { platform: 'pixiv', sourceId: String(b.illustId), meta, files, sourceTabId: tabId }
}

function validateWork(work) {
  if (!['pixiv', 'x'].includes(work.platform) || !/^\d+$/.test(work.sourceId) || !work.meta || !Array.isArray(work.files) || !work.files.length || work.files.length > 10000) throw new Error('作品信息无效')
  if (JSON.stringify(work).length > 4 * 1024 * 1024) throw new Error('作品元数据过大')
  for (const file of work.files) {
    file.url = mediaURL(file.url, work.platform)
    if (!/^(jpg|jpeg|png|gif|webp|mp4|webm|ugoira)$/.test(file.ext)) throw new Error('文件格式不支持')
    if (file.path && (file.path.startsWith('/') || file.path.includes('\\') || file.path.split('/').some(v => v === '..' || v === '.'))) throw new Error('文件路径无效')
  }
  return work
}
async function submit(works, requestId, origin, bookmark) {
  if (!requestId || requestId.length > 128 || !Array.isArray(works) || !works.length || works.length > 1000) throw new Error('任务内容无效')
  const key = `${origin}:${requestId}`, old = await read('requests', key)
  const signature = JSON.stringify(works)
  if (old) { if (old.signature !== signature) throw new Error('同一任务标识对应了不同作品'); await runner(); return { accepted: true, ids: old.ids } }
  const config = await settings(), ids = [], tasks = []
  for (const work of works.map(validateWork)) {
    const id = crypto.randomUUID(); ids.push(id)
    tasks.push({ id, batch: key, source: origin, work, destination: config.destination, status: config.destination === 'ask' ? 'awaitingDestination' : 'queued', local: 'pending', upload: 'pending', createdAt: new Date().toISOString(), requestId: crypto.randomUUID(), bookmarkAfterSave: bookmark ? bookmark.enabled === true : origin === 'page' && work.platform === 'pixiv' && config.bookmarkAfterSave, ...(bookmark ? { bookmarkTags: bookmark.withTags ? (bookmark.translated ? work.meta.tagsTranslOnly || work.meta.tags : work.meta.tags) : [], bookmarkRestrict: bookmark.private === true } : {}) })
  }
  const saved = await saveBatch({ id: key, ids, signature }, tasks)
  await runner()
  if (config.destination === 'ask') await chrome.tabs.create({ url: chrome.runtime.getURL('tasks.html') })
  return { accepted: true, ids: saved.ids }
}
async function batchStatus(requestId, origin) {
  const batch = await read('requests', `${origin}:${requestId}`)
  if (!batch) return { found: false, tasks: [] }
  return { found: true, tasks: await Promise.all(batch.ids.map(async id => { const task = await read('tasks', id); return { id, sourceId: task.work.sourceId, status: task.status, error: task.error || '', files: task.status === 'completed' ? task.files.filter(file => file.id).map(file => ({ id: file.id, path: file.path })) : [] } })) }
}
async function control(ids, action, destination) {
  if (!Array.isArray(ids) || ids.length > 1000) throw new Error('任务列表无效')
  for (const id of ids) {
    const task = await read('tasks', id)
    if (!task || task.status === 'completed' || task.status === 'cancelled') continue
    if (action === 'destination' && ['local', 'artkeep', 'both'].includes(destination)) await updateTask(id, { destination, status: 'queued', error: '' })
    else if (action === 'pause' && task.status !== 'awaitingDestination') await updateTask(id, { status: 'paused' })
    else if (action === 'cancel') await updateTask(id, { status: 'cancelled' })
    else if (action === 'resume' && task.status !== 'awaitingDestination' && task.status !== 'running') await updateTask(id, { status: 'queued', error: '' })
  }
  await runner()
}
async function handle(message, sender) {
  if (message.target === 'engine') return
  const trusted = sender.url?.startsWith(chrome.runtime.getURL(''))
  const page = /^https:\/\/(www\.pixiv\.net|x\.com|twitter\.com)\//.test(sender.url || '')
  if (message.type === 'config') { const s = await settings(); return { takeover: s.takeover } }
  if (message.type === 'ppd' && page && sender.url.startsWith('https://www.pixiv.net/')) {
    const origin = 'ppd'
    if (!/^[a-zA-Z0-9-]{1,128}$/.test(message.requestId || '')) throw new Error('PPD 任务标识无效')
    if (message.action === 'submit') {
      if (!(await settings()).takeover) throw new Error('PPD 接管已关闭')
      for (const work of message.works || []) { if (work.platform !== 'pixiv') throw new Error('PPD 作品来源无效'); work.sourceTabId = sender.tab.id }
      return submit(message.works, message.requestId, origin, message.bookmark || {})
    }
    if (message.action === 'status') return batchStatus(message.requestId, origin)
    if (message.action === 'pause' || message.action === 'resume' || message.action === 'cancel') {
      const batch = await read('requests', `${origin}:${message.requestId}`)
      if (!batch) throw new Error('PPD 收藏任务不存在')
      return control(batch.ids, message.action)
    }
    throw new Error('PPD 操作不支持')
  }
  if (message.type === 'submitPixiv' && page && sender.url.includes('pixiv.net/')) {
    const works = []
    for (const id of message.ids || []) { if (!/^\d+$/.test(id)) throw new Error('作品 ID 无效'); works.push(await pixiv(sender.tab.id, id)) }
    return submit(works, message.requestId, 'page')
  }
  if (message.type === 'submitX' && page && !sender.url.includes('pixiv.net/')) return submit(message.works, message.requestId, 'page')
  if (message.type === 'parseX' && page && !sender.url.includes('pixiv.net/')) {
    if (!Array.isArray(message.tweets) || message.tweets.length > 200) throw new Error('帖子数据无效')
    return message.tweets.map(value => { try { return extractTweet(value) } catch { return null } }).filter(Boolean)
  }
  if (message.type === 'openTasks' && (trusted || page)) { await chrome.tabs.create({ url: chrome.runtime.getURL('tasks.html') }); return true }
  if (!trusted) throw new Error('操作来源无效')
  if (message.type === 'engineSettings') return settings()
  if (message.type === 'bookmark') {
    const task = await read('tasks', message.id)
    if (!task?.bookmarkAfterSave || task.work.platform !== 'pixiv') throw new Error('收藏操作无效')
    const tabs = await chrome.tabs.query({ url: 'https://www.pixiv.net/*' })
    const tab = tabs.find(tab => tab.id === task.work.sourceTabId) || tabs[0]
    if (!tab) throw new Error('文件已保存，请打开 Pixiv 页面后重试原作收藏')
    const config = await settings()
    const [response] = await chrome.scripting.executeScript({ target: { tabId: tab.id }, world: 'MAIN', func: async (id, tags, restrict) => {
      const before = await fetch('/ajax/illust/' + id, { credentials: 'same-origin' })
      if (!before.ok) throw new Error('无法确认 Pixiv 收藏状态，请重新加载页面')
      const info = await before.json()
      if (info.error) throw new Error(info.message || 'Pixiv 作品不可用')
      if (info.body.bookmarkData) return true
      const page = await fetch('/artworks/' + id, { credentials: 'same-origin' })
      const html = await page.text()
      const token = html.match(/token[\\]*":[\\]*"([a-z0-9]{32})/i)?.[1]
      if (!token) throw new Error('Pixiv 登录已失效，请重新登录后重试')
      const result = await fetch('/ajax/illusts/bookmarks/add', { method: 'POST', credentials: 'same-origin', headers: {'Content-Type':'application/json','x-csrf-token':token}, body: JSON.stringify({ illust_id:id, comment:'', restrict, tags }) })
      const value = await result.json()
      if (!result.ok || value.error) throw new Error(value.message || 'Pixiv 收藏失败，请检查原作后重试')
      return true
    }, args: [task.work.sourceId, task.bookmarkTags ?? task.work.meta.tags, (task.bookmarkRestrict ?? config.bookmarkRestrict) ? 1 : 0] })
    if (!response?.result) throw new Error('Pixiv 收藏状态未知，请检查原作后重试')
    await updateTask(task.id, { bookmark: 'completed' })
    return true
  }
  if (message.type === 'list') return read('tasks')
  if (message.type === 'control') return control(message.ids, message.action, message.destination)
  if (message.type === 'saveLocal') {
    const task = await read('tasks', message.id)
    if (!task || task.files?.[message.index]?.path !== message.path || !message.url?.startsWith(`blob:chrome-extension://${chrome.runtime.id}/`)) throw new Error('本地保存请求无效')
    const previous = task.files[message.index].downloadId
    if (previous !== undefined) {
      const [item] = await chrome.downloads.search({ id: previous })
      if (item?.state === 'complete') return { id: previous, complete: true }
      if (item?.state === 'in_progress') return { id: previous }
    }
    const oldURL = task.files[message.index].downloadURL
    if (oldURL) {
      const urlRegex = '^' + oldURL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$'
      const [item] = await chrome.downloads.search({ urlRegex, limit: 1 })
      if (item && ['complete', 'in_progress'].includes(item.state)) {
        const files = task.files.map((f, i) => i === message.index ? { ...f, downloadId: item.id } : f)
        await updateTask(task.id, { files })
        return { id: item.id, complete: item.state === 'complete' }
      }
    }
    task.files[message.index].downloadURL = message.url
    await updateTask(task.id, { files: task.files })
    const id = await chrome.downloads.download({ url: message.url, filename: message.path, saveAs: false, conflictAction: 'overwrite' })
    const files = task.files.map((f, i) => i === message.index ? { ...f, downloadId: id } : f)
    await updateTask(task.id, { files })
    return { id }
  }
  if (message.type === 'downloadStatus') return chrome.downloads.search({ id: message.id })
  if (message.type === 'wake') return runner()
  throw new Error('操作不支持')
}
chrome.runtime.onMessage.addListener((message, sender, reply) => {
  if (message.target === 'engine') return false
  handle(message, sender).then(value => reply({ ok: true, value }), error => reply({ ok: false, error: error.message }))
  return true
})
async function recover() {
  for (const t of await read('tasks')) if (t.status === 'running') await updateTask(t.id, { status: 'queued' })
  await runner()
}
chrome.runtime.onInstalled.addListener(async () => { await chrome.storage.local.setAccessLevel({ accessLevel: 'TRUSTED_CONTEXTS' }); await chrome.alarms.create('resume', { periodInMinutes: 1 }); await recover() })
chrome.runtime.onStartup.addListener(recover)
chrome.alarms.onAlarm.addListener(alarm => { if (alarm.name === 'resume') runner().catch(() => {}) })
