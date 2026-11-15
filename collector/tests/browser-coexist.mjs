import fs from 'node:fs/promises'
import path from 'node:path'
import assert from 'node:assert/strict'

const root = process.cwd(), collector = path.join(root, 'collector')
const ppd = process.env.ARTKEEP_TEST_PPD_NORMAL || path.join(root, 'artifacts', 'ppd-normal')
await fs.access(path.join(ppd, 'manifest.json'))
const response = await fetch('http://127.0.0.1:9517/session', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ capabilities: { alwaysMatch: { browserName: 'MicrosoftEdge', 'ms:edgeOptions': { binary: 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe', args: ['--headless=new', '--no-first-run', '--disable-background-networking', '--disable-extensions-except=' + collector + ',' + ppd, '--load-extension=' + collector + ',' + ppd, '--user-data-dir=' + path.join(root, 'artifacts', 'edge-normal-ppd-profile')] } } } }) })
const created = await response.json()
assert.ok(response.ok, JSON.stringify(created))
const base = 'http://127.0.0.1:9517/session/' + created.value.sessionId
async function driver(endpoint, data) {
  const result = await fetch(base + endpoint, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) })
  const value = await result.json()
  assert.ok(result.ok, JSON.stringify(value))
  return value.value
}
let socket
try {
  const address = created.value.capabilities['ms:edgeOptions'].debuggerAddress
  const targets = await (await fetch(`http://${address}/json/list`)).json()
  assert.ok(targets.some(target => target.type === 'service_worker' && target.url.endsWith('/js/background.js')), '普通 PPD 后台未加载')
  const target = targets.find(target => target.type === 'page')
  socket = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise(resolve => socket.addEventListener('open', resolve, { once: true }))
  let sequence = 0
  const pending = new Map()
  const call = (method, params = {}) => new Promise((resolve, reject) => { const id = ++sequence; pending.set(id, { resolve, reject }); socket.send(JSON.stringify({ id, method, params })) })
  socket.addEventListener('message', event => {
    const value = JSON.parse(event.data)
    if (value.id) { const request = pending.get(value.id); pending.delete(value.id); value.error ? request?.reject(new Error(value.error.message)) : request?.resolve(value.result); return }
    if (value.method !== 'Fetch.requestPaused') return
    const request = value.params
    if (request.resourceType === 'Document') call('Fetch.fulfillRequest', { requestId: request.requestId, responseCode: 200, responseHeaders: [{ name: 'Content-Type', value: 'text/html; charset=utf-8' }], body: Buffer.from('<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Pixiv 并存验收</title><body><a href="/artworks/123"><img alt="测试作品"></a></body></html>').toString('base64') }).catch(() => {})
    else call('Fetch.failRequest', { requestId: request.requestId, errorReason: 'Aborted' }).catch(() => {})
  })
  await call('Fetch.enable', { patterns: [{ urlPattern: 'http*' }] })
  await driver('/url', { url: 'https://www.pixiv.net/artworks/123' })
  let result
  for (let i = 0; i < 40; i++) {
    result = await driver('/execute/sync', { script: 'const bar=document.querySelector("#artkeep-collector-bar");return {independent:!!bar&&!bar.hidden,ppd:!!document.querySelector(".settingForm"),compatible:document.documentElement.dataset.artkeepPpd}', args: [] })
    if (result.independent && result.ppd && result.compatible === 'ready') break
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  assert.equal(result.independent, true)
  assert.equal(result.ppd, true)
  assert.equal(result.compatible, 'ready')
  await call('Fetch.disable')
  console.log('普通 PPD 并存：PPD 设置面板与 Artkeep 独立收藏入口同时加载（受控 Pixiv 页面）')
} finally {
  socket?.close()
  await fetch(base, { method: 'DELETE' })
}
