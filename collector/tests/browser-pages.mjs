import fs from 'node:fs/promises'
import path from 'node:path'
import assert from 'node:assert/strict'

const session = JSON.parse(await fs.readFile('artifacts/webdriver/session.json', 'utf8')).id
const base = `http://127.0.0.1:9517/session/${session}`
const server = process.env.ARTKEEP_TEST_SERVER || 'http://127.0.0.1:28080'
const extensionId = (await fs.readFile('collector/extension-id.txt', 'utf8')).trim()
const startedAt = new Date().toISOString()
const sessions = await (await fetch('http://127.0.0.1:9517/sessions')).json()
const address = sessions.value.find(item => item.id === session).capabilities['ms:edgeOptions'].debuggerAddress
async function driver(endpoint, data, method = 'POST') {
  const response = await fetch(base + endpoint, { method, ...(data === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) }) })
  const value = await response.json()
  if (!response.ok) throw new Error(value.value?.message || JSON.stringify(value))
  return value.value
}
async function script(fn, ...args) {
  const value = await driver('/execute/async', { script: 'const done=arguments[arguments.length-1];(' + fn.toString() + ')(...Array.from(arguments).slice(0,-1)).then(value=>done({value}),error=>done({error:error.stack}));', args })
  if (value.error) throw new Error(value.error)
  return value.value
}
async function until(fn) {
  for (let i = 0; i < 60; i++) { const value = await fn(); if (value) return value; await new Promise(resolve => setTimeout(resolve, 250)) }
  throw new Error('等待浏览器状态超时')
}
async function connect(target) {
  const ws = new WebSocket(target.webSocketDebuggerUrl), requests = new Map()
  let sequence = 0, listener = () => {}
  await new Promise((resolve, reject) => { ws.addEventListener('open', resolve, { once: true }); ws.addEventListener('error', reject, { once: true }) })
  ws.addEventListener('message', event => {
    const value = JSON.parse(event.data)
    if (value.id) { const request = requests.get(value.id); requests.delete(value.id); value.error ? request?.reject(new Error(value.error.message)) : request?.resolve(value.result) }
    else listener(value)
  })
  return {
    call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++sequence; requests.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params })) }) },
    listen(fn) { listener = fn }, close() { ws.close() },
  }
}

await driver('/url', { url: `chrome-extension://${extensionId}/options.html` })
await script(async () => {
  const { settings } = await chrome.storage.local.get('settings')
  await chrome.storage.local.set({ settings: { ...settings, destination: 'ask' } })
  return true
})

const pageTarget = (await (await fetch(`http://${address}/json/list`)).json()).find(item => item.type === 'page' && item.url.includes('/options.html'))
const page = await connect(pageTarget)
const tweets = [1, 2].map(number => ({ rest_id: `9999900${number}`, legacy: { full_text: `X 多选验收 ${number}`, created_at: 'Mon Nov 16 00:00:00 +0000 2026', entities: { hashtags: [] }, extended_entities: { media: number === 1 ? [{ type: 'photo', media_url_https: 'https://pbs.twimg.com/media/artkeep.jpg' }] : [{ type: 'animated_gif', video_info: { variants: [{ content_type: 'video/mp4', bitrate: 10, url: 'https://video.twimg.com/low.mp4' }, { content_type: 'video/mp4', bitrate: 100, url: 'https://video.twimg.com/high.mp4' }] } }] } }, core: { user_results: { result: { rest_id: '99999', legacy: { name: '验收作者', screen_name: 'artkeep_test' } } } } }))
const html = '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>X 多选验收</title><body>' + tweets.map(tweet => `<article data-testid="tweet"><a href="/artkeep_test/status/${tweet.rest_id}"><time>2026-11-16</time></a></article>`).join('') + '<script>const tweets=' + JSON.stringify(tweets) + ';document.querySelectorAll("article").forEach((article,index)=>article.__reactProps$artkeep={tweet:tweets[index]});</script></body></html>'
page.listen(event => {
  if (event.method !== 'Fetch.requestPaused') return
  const params = event.params
  const command = params.resourceType === 'Document' && params.request.url.startsWith('https://x.com/') ? page.call('Fetch.fulfillRequest', { requestId: params.requestId, responseCode: 200, responseHeaders: [{ name: 'Content-Type', value: 'text/html; charset=utf-8' }], body: Buffer.from(html).toString('base64') }) : page.call('Fetch.failRequest', { requestId: params.requestId, errorReason: 'Aborted' })
  command.catch(() => {})
})
await page.call('Fetch.enable', { patterns: [{ urlPattern: 'http*' }] })
await driver('/url', { url: 'https://x.com/artkeep-acceptance' })
await until(() => driver('/execute/sync', { script: 'document.dispatchEvent(new Event("artkeep-x-inspect")); return !!document.querySelector("#artkeep-collector-bar")', args: [] }))
await new Promise(resolve => setTimeout(resolve, 1000))
await driver('/execute/sync', { script: 'const root=document.querySelector("#artkeep-collector-bar").shadowRoot;Array.from(root.querySelectorAll("button")).find(b=>b.textContent==="选择已加载作品").click();document.querySelectorAll("[data-artkeep-select]").forEach(input=>input.click());root.querySelector("button").click();return true', args: [] })
await until(() => driver('/execute/sync', { script: 'return document.querySelector("#artkeep-collector-bar").shadowRoot.textContent.includes("已加入收藏任务")', args: [] }))
await page.call('Fetch.disable'); page.close()
await driver('/url', { url: `chrome-extension://${extensionId}/tasks.html` })
const selected = await script(async startedAt => { const { read } = await import('./db.js'); return (await read('tasks')).filter(task => task.work.platform === 'x' && task.createdAt >= startedAt) }, startedAt)
assert.equal(selected.length, 2)
assert.ok(selected.every(task => task.status === 'awaitingDestination'))
assert.ok(selected.find(task => task.work.sourceId === '99999001').work.files[0].url.includes('name=orig'))
assert.ok(selected.find(task => task.work.sourceId === '99999002').work.files[0].url.endsWith('/high.mp4'))
await script(async ids => { await chrome.runtime.sendMessage({ type: 'control', ids, action: 'cancel' }); return true }, selected.map(task => task.id))
console.log('X 页面：已加载帖子多选、每次询问、原图与最高码率动画通过（受控页面数据）')

const login = await fetch(server + '/api/v1/sessions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ scope: 'admin', password: process.env.ARTKEEP_TEST_PASSWORD || 'artkeep-validation-only' }) })
assert.equal(login.status, 201)
const cookie = login.headers.get('set-cookie').split(';')[0]
async function api(endpoint, method = 'GET', body) {
  const response = await fetch(server + '/api/v1' + endpoint, { method, headers: { Cookie: cookie, ...(body ? { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) })
  const result = await response.json()
  assert.ok(response.ok, JSON.stringify(result))
  return result
}
const job = await api('/jobs', 'POST', { kind: 'import', source: 'server', path: path.resolve('artifacts/browser-fixtures'), format: 'auto' })
await until(async () => { const current = await api('/jobs/' + job.id); if (['failed', 'partial'].includes(current.status)) throw new Error(JSON.stringify(current)); return current.status === 'completed' })
const source = await api('/sources/pixiv/artwork/999999991')
assert.equal(source.collected, true)
await driver('/url', { url: `${server}/works/${source.workId}` })
await driver('/ms/cdp/execute', { cmd: 'Page.bringToFront', params: {} })
const playing = await until(() => script(async () => {
  const video = document.querySelector('video')
  if (!video) return false
  video.muted = true; await video.play()
  return { source: video.currentSrc, width: video.videoWidth, height: video.videoHeight, paused: video.paused }
}))
assert.ok(new URL(playing.source).pathname.endsWith('/playback'))
assert.equal(playing.width, 16); assert.equal(playing.height, 12); assert.equal(playing.paused, false)
console.log('动图详情页：16×12 双帧 .ugoira 已使用 MP4 播放资源播放')
await script(async () => { document.querySelector('[aria-label="全屏查看"]').click(); return true })
const fullscreen = await until(() => script(async () => {
  const video = document.querySelector('.archive-lightbox video')
  if (!video) return false
  video.muted = true; await video.play()
  return { source: video.currentSrc, width: video.videoWidth, paused: video.paused }
}))
assert.ok(new URL(fullscreen.source).pathname.endsWith('/playback'))
assert.equal(fullscreen.width, 16); assert.equal(fullscreen.paused, false)
console.log('动图全屏查看：MP4 播放通过')
