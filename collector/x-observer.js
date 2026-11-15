(() => {
  const seen = new WeakSet()
  function publish(value, depth = 0, found = []) {
    if (!value || typeof value !== 'object' || depth > 20 || seen.has(value) || found.length >= 200) return found
    seen.add(value)
    if (value.legacy && value.rest_id && (value.legacy.full_text !== undefined || value.legacy.text !== undefined)) {
      try { found.push(JSON.parse(JSON.stringify({ rest_id: value.rest_id, legacy: value.legacy, core: value.core, note_tweet: value.note_tweet }))) } catch { /* 等待完整的接口响应。 */ }
    }
    for (const key of Object.keys(value)) {
      if (['return', '_owner', 'stateNode', 'alternate', 'child', 'sibling'].includes(key)) continue
      publish(value[key], depth + 1, found)
    }
    return found
  }
  function send(value) { const tweets = publish(value); if (tweets.length) window.postMessage({ channel: 'artkeep-x-data', tweets }, location.origin) }
  const original = window.fetch
  window.fetch = async function (...args) {
    const response = await original.apply(this, args)
    const url = typeof args[0] === 'string' ? args[0] : args[0]?.url
    if (url && /\/graphql\//.test(url)) response.clone().json().then(send).catch(() => {})
    return response
  }
  const open = XMLHttpRequest.prototype.open, sendXHR = XMLHttpRequest.prototype.send
  XMLHttpRequest.prototype.open = function (method, url, ...args) { this.artkeepObserve = /\/graphql\//.test(url); return open.call(this, method, url, ...args) }
  XMLHttpRequest.prototype.send = function (...args) {
    if (this.artkeepObserve) this.addEventListener('load', () => { try { send(this.responseType === 'json' ? this.response : JSON.parse(this.responseText)) } catch { /* 忽略非 JSON 响应。 */ } }, { once: true })
    return sendXHR.apply(this, args)
  }
  document.addEventListener('artkeep-x-inspect', () => {
    for (const article of document.querySelectorAll('article[data-testid="tweet"]')) {
      let node = article
      for (let i = 0; node && i < 8; i++, node = node.parentElement) {
        const key = Object.keys(node).find(key => key.startsWith('__reactProps') || key.startsWith('__reactFiber'))
        if (key) { let fiber = node[key]; for (let j = 0; fiber && j < 12; j++, fiber = fiber.return) send(fiber.memoizedProps || fiber) }
      }
    }
  })
})()
