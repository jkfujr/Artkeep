(() => {
  const pixiv = location.hostname === 'www.pixiv.net', tweets = new Map(), selected = new Set()
  let config, toolbar, status, action, selectButton, queued = false, requestId = crypto.randomUUID()
  async function message(data) { const r = await chrome.runtime.sendMessage(data); if (!r?.ok) throw new Error(r?.error || '扩展暂不可用，请重新加载页面'); return r.value }
  const isTakeover = () => pixiv && config?.takeover && document.documentElement.dataset.artkeepPpd === 'ready'
  function getId(article) {
    const time = article.querySelector('time')
    return (time?.closest('a')?.getAttribute('href') || '').match(/\/status\/(\d+)/)?.[1]
  }
  function setStatus(text) { if (status) status.textContent = text }
  function currentIDs() {
    const id = pixiv ? location.pathname.match(/\/artworks\/(\d+)/)?.[1] : location.pathname.match(/\/status\/(\d+)/)?.[1]
    return id ? [id] : []
  }
  async function collect(ids) {
    if (queued || !ids.length) return
    queued = true; action.disabled = true; setStatus('正在加入任务')
    try {
      if (pixiv) await message({ type: 'submitPixiv', ids, requestId })
      else {
        const works = ids.map(id => tweets.get(id))
        if (works.some(work => !work)) { document.dispatchEvent(new Event('artkeep-x-inspect')); throw new Error('帖子数据尚未加载，请重新加载帖子后重试') }
        await message({ type: 'submitX', works, requestId })
      }
      setStatus('已加入收藏任务'); selected.clear(); document.querySelectorAll('[data-artkeep-select]').forEach(input => { input.checked = false }); requestId = crypto.randomUUID()
    } catch (error) { setStatus(error.message) } finally { queued = false; action.disabled = false }
  }
  function scan() {
    if (!document.body || !config) return
    if (!toolbar) {
      toolbar = document.createElement('aside'); toolbar.id = 'artkeep-collector-bar'; toolbar.setAttribute('aria-label', '收藏作品')
      const root = toolbar.attachShadow({ mode: 'open' })
      const style = document.createElement('style'); style.textContent = ':host{position:fixed;bottom:16px;right:16px;z-index:2147483600;color-scheme:light dark;font:14px/1.5 system-ui}div{max-width:360px;padding:10px;display:flex;gap:8px;align-items:center;flex-wrap:wrap;background:light-dark(#fff,#20242b);color:light-dark(#20242b,#f1f4f9);border:1px solid light-dark(#c7ced9,#647085);border-radius:8px;box-shadow:0 4px 16px #0002}button{font:inherit;min-height:40px;padding:6px 12px;cursor:pointer;border-radius:5px;border:1px solid #74819a;background:light-dark(#f3f5fa,#31394a);color:inherit}button:focus-visible{outline:2px solid #3985df;outline-offset:2px}span{flex-basis:100%;overflow-wrap:anywhere}button:disabled{opacity:.6;cursor:wait}'
      const panel = document.createElement('div'); action = document.createElement('button'); action.textContent = '收藏当前作品'; action.onclick = () => collect(selected.size ? [...selected] : currentIDs())
      selectButton = document.createElement('button'); selectButton.textContent = '选择已加载作品'; selectButton.onclick = () => { document.documentElement.toggleAttribute('data-artkeep-select-mode'); scan(); document.dispatchEvent(new Event('artkeep-x-inspect')) }
      const tasks = document.createElement('button'); tasks.textContent = '任务'; tasks.onclick = () => message({ type: 'openTasks' }).catch(e => setStatus(e.message))
      status = document.createElement('span'); status.setAttribute('role', 'status'); panel.append(action, selectButton, tasks, status); root.append(style, panel); document.body.append(toolbar)
    }
    toolbar.hidden = isTakeover()
    const actionText = selected.size ? `收藏所选（${selected.size}）` : '收藏当前作品'; if (action.textContent !== actionText) action.textContent = actionText
    const selection = document.documentElement.hasAttribute('data-artkeep-select-mode')
    for (const check of document.querySelectorAll('[data-artkeep-select]')) check.hidden = !selection
    for (const old of document.querySelectorAll('[data-artkeep-actions]')) old.hidden = isTakeover() || (!selection && old.dataset.artkeepActions === 'selectOnly')
    if (isTakeover()) return
    const nodes = pixiv ? document.querySelectorAll('a[href*="/artworks/"]') : document.querySelectorAll('article[data-testid="tweet"]')
    const done = new Set()
    for (const node of nodes) {
      const id = pixiv ? node.getAttribute('href').match(/\/artworks\/(\d+)/)?.[1] : getId(node)
      if (!id || done.has(id) || pixiv && !node.querySelector('img')) continue
      done.add(id)
      const parent = pixiv ? node.parentElement : node
      if (parent.querySelector(`[data-artkeep-id="${id}"]`)) continue
      const controls = document.createElement('span'); controls.dataset.artkeepActions = ''; controls.dataset.artkeepId = id
      controls.style.cssText = 'display:inline-flex;gap:8px;align-items:center;position:relative;z-index:20;padding:4px;font:13px/1.5 system-ui'
      const check = document.createElement('input'); check.type = 'checkbox'; check.dataset.artkeepSelect = id; check.setAttribute('aria-label', `选择作品 ${id}`); check.style.cssText = 'width:20px;height:20px;accent-color:#2879cd'; check.checked = selected.has(id); check.onchange = () => { check.checked ? selected.add(id) : selected.delete(id); action.textContent = selected.size ? `收藏所选（${selected.size}）` : '收藏当前作品' }
      const button = document.createElement('button'); button.type = 'button'; button.textContent = '收藏'; button.style.cssText = 'min-height:32px;padding:3px 10px;border:1px solid #74819a;border-radius:4px;color:light-dark(#182437,#f0f4ff);background:light-dark(#fff,#273141);color-scheme:light dark;cursor:pointer'; button.onclick = e => { e.preventDefault(); e.stopPropagation(); collect([id]) }
      check.hidden = !selection
      controls.append(check, button); controls.onclick = e => e.stopPropagation(); parent.append(controls)
    }
  }
  if (!pixiv) window.addEventListener('message', async event => {
    if (event.source !== window || event.origin !== location.origin || event.data?.channel !== 'artkeep-x-data' || !Array.isArray(event.data.tweets)) return
    try { const works = await message({ type: 'parseX', tweets: event.data.tweets }); for (const work of works) tweets.set(work.sourceId, work) } catch (e) { setStatus(e.message) }
  })
  let scheduled
  const observer = new MutationObserver(() => { clearTimeout(scheduled); scheduled = setTimeout(scan, 150) })
  observer.observe(document.documentElement, { subtree: true, childList: true })
  message({ type: 'config' }).then(value => { config = value; scan() }).catch(() => {})
  setInterval(() => { message({ type: 'config' }).then(value => { config = value; scan() }).catch(() => {}); if (!pixiv) document.dispatchEvent(new Event('artkeep-x-inspect')) }, 6000)
})()
