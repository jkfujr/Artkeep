import fs from 'node:fs/promises'
import path from 'node:path'
import assert from 'node:assert/strict'

const source = process.argv[2]
if (!source) throw new Error('请提供实际样例作品目录')
const server = process.env.ARTKEEP_TEST_SERVER || 'http://127.0.0.1:28080'
const root = process.cwd(), collector = path.join(root, 'collector'), ppd = path.join(root, 'artifacts/ppd-normal')
const extensionId = (await fs.readFile('collector/extension-id.txt', 'utf8')).trim()
const entries = await fs.readdir(source), meta = JSON.parse(await fs.readFile(path.join(source, entries.find(name => name.endsWith('-meta.json'))), 'utf8'))
const jsonFile = path.join(root, 'artifacts/ppd-browser-result.json')
await fs.writeFile(jsonFile, JSON.stringify([meta]))
const created = await (await fetch('http://127.0.0.1:9517/session', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ capabilities: { alwaysMatch: { browserName: 'MicrosoftEdge', 'ms:edgeOptions': { binary: 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe', args: ['--headless=new', '--no-first-run', '--disable-background-networking', '--disable-extensions-except=' + collector + ',' + ppd, '--load-extension=' + collector + ',' + ppd, '--user-data-dir=' + path.join(root, 'artifacts/edge-official-ppd-' + Date.now())] } } } }) })).json()
assert.ok(created.value.sessionId, JSON.stringify(created))
const base = 'http://127.0.0.1:9517/session/' + created.value.sessionId
await fs.writeFile('artifacts/webdriver/session.json', JSON.stringify({ id: created.value.sessionId }))
async function driver(endpoint, data, method = 'POST') {
  const response = await fetch(base + endpoint, { method, ...(data === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) }) })
  const result = await response.json()
  assert.ok(response.ok, JSON.stringify(result))
  return result.value
}
async function script(fn, ...args) {
  const result = await driver('/execute/async', { script: 'const done=arguments[arguments.length-1];(' + fn.toString() + ')(...Array.from(arguments).slice(0,-1)).then(value=>done({value}),error=>done({error:error.stack}));', args })
  if (result.error) throw new Error(result.error)
  return result.value
}
async function until(fn, label) {
  for (let i = 0; i < 100; i++) { const value = await fn(); if (value) return value; await new Promise(resolve => setTimeout(resolve, 200)) }
  throw new Error('等待超时：' + label)
}

const login = await fetch(server + '/api/v1/sessions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ scope: 'admin', password: process.env.ARTKEEP_TEST_PASSWORD || 'artkeep-validation-only' }) })
assert.equal(login.status, 201)
const issued = await fetch(server + '/api/v1/access-tokens', { method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: login.headers.get('set-cookie').split(';')[0] }, body: JSON.stringify({ name: '官方 PPD 兼容检查', origin: 'chrome-extension://' + extensionId }) })
const token = (await issued.json()).token
assert.equal(issued.status, 201)
await driver('/url', { url: `chrome-extension://${extensionId}/options.html` })
await script(async (server, token) => { await chrome.storage.local.set({ settings: { server, token, takeover: true, destination: 'ask', naming: 'pixiv/{user_id}-{user}/{pid}-{title}/{id}', downloadThread: 1, downloadInterval: 0 } }); return true }, server, token)
const nativeWindow = await driver('/window', undefined, 'GET')
const address = created.value.capabilities['ms:edgeOptions'].debuggerAddress
const target = (await (await fetch(`http://${address}/json/list`)).json()).find(target => target.type === 'page' && target.url.includes('/options.html'))
const socket = new WebSocket(target.webSocketDebuggerUrl)
await new Promise(resolve => socket.addEventListener('open', resolve, { once: true }))
let sequence = 0, chooser
const requests = new Map(), nativeMedia = [], bookmarks = []
const cdp = (method, params = {}) => new Promise((resolve, reject) => { const id = ++sequence; requests.set(id, { resolve, reject }); socket.send(JSON.stringify({ id, method, params })) })
socket.addEventListener('message', event => {
  const value = JSON.parse(event.data)
  if (value.id) { const request = requests.get(value.id); requests.delete(value.id); value.error ? request?.reject(new Error(value.error.message)) : request?.resolve(value.result); return }
  if (value.method === 'Page.fileChooserOpened') { chooser = value.params; return }
  if (value.method === 'Page.javascriptDialogOpening') { cdp('Page.handleJavaScriptDialog', { accept: true }).catch(() => {}); return }
  if (value.method !== 'Fetch.requestPaused') return
  const request = value.params
  if (request.request.url.includes('pximg.net')) nativeMedia.push(request.request.url)
  if (request.request.url.includes('/ajax/illusts/bookmarks/add')) {
    bookmarks.push(JSON.parse(request.request.postData))
    cdp('Fetch.fulfillRequest', { requestId: request.requestId, responseCode: 200, responseHeaders: [{ name: 'Content-Type', value: 'application/json' }], body: Buffer.from('{"error":false,"body":{}}').toString('base64') }).catch(() => {})
  } else if (request.request.url.includes('/ajax/illust/')) cdp('Fetch.fulfillRequest', { requestId: request.requestId, responseCode: 200, responseHeaders: [{ name: 'Content-Type', value: 'application/json' }], body: Buffer.from('{"error":false,"body":{"bookmarkData":null}}').toString('base64') }).catch(() => {})
  else if (request.resourceType === 'Document' || request.request.url.includes('/artworks/')) cdp('Fetch.fulfillRequest', { requestId: request.requestId, responseCode: 200, responseHeaders: [{ name: 'Content-Type', value: 'text/html; charset=utf-8' }], body: Buffer.from('<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>官方 PPD 兼容检查</title><body><script type="application/json">{"token":"0123456789abcdef0123456789abcdef"}</script></body></html>').toString('base64') }).catch(() => {})
  else cdp('Fetch.failRequest', { requestId: request.requestId, errorReason: 'Aborted' }).catch(() => {})
})
await cdp('Fetch.enable', { patterns: [{ urlPattern: 'http*' }] })
await cdp('Page.enable')
await cdp('Page.setInterceptFileChooserDialog', { enabled: true })
await cdp('Page.addScriptToEvaluateOnNewDocument', { source: 'window.artkeepPpdReady=false;window.addEventListener("settingInitialized",()=>window.artkeepPpdReady=true);' })
await driver('/url', { url: `https://www.pixiv.net/artworks/${meta.idNum}` })
await until(() => script(async () => window.artkeepPpdReady && !!document.querySelector('form.settingForm') && document.documentElement.dataset.artkeepPpd === 'ready' && (await indexedDB.databases()).some(db => db.name === 'PBD')), 'PPD 与 Artkeep 页面入口')
await script(async () => {
  const input = document.querySelector('form.settingForm').elements.namedItem('autoStartDownload')
  if (input.checked) input.click()
  const form = document.querySelector('form.settingForm')
  const bookmark = form.elements.namedItem('bmkAfterDL'); if (!bookmark.checked) bookmark.click()
  for (const [name, value] of [['withTag', 'transl'], ['restrict', 'yes']]) {
    const input = [...form.elements.namedItem(name)].find(input => input.value === value)
    input.click()
  }
  window.artkeepNativeComplete = 0
  window.addEventListener('downloadComplete', () => window.artkeepNativeComplete++)
  return true
})
await cdp('Runtime.evaluate', { expression: 'document.getElementById("importCrawlResults").click()', userGesture: true })
await until(() => chooser, 'PPD 导入文件选择')
await cdp('DOM.setFileInputFiles', { backendNodeId: chooser.backendNodeId, files: [jsonFile] })
await until(() => script(async () => !!document.getElementById('startDownload') && document.body.innerText.includes('5')), 'PPD 抓取结果导入')
const preview = await script(async () => {
  const naming = document.querySelector('textarea[name="userSetName"]')
  naming.value = 'pixiv/{user_id}/{bmk}/{id}'; naming.dispatchEvent(new Event('change', { bubbles: true }))
  let content
  const capture = event => { if (event.detail?.data?.title === '_预览文件名') { event.stopImmediatePropagation(); content = event.detail.data.content } }
  window.addEventListener('output', capture, true)
  window.dispatchEvent(new CustomEvent('previewFileName', { detail: { data: {} } }))
  window.removeEventListener('output', capture, true)
  return new DOMParser().parseFromString(content, 'text/html').body.textContent
})
await script(async () => { document.getElementById('startDownload').click(); return true })
await until(() => script(async () => (document.getElementById('artkeep-ppd-status')?.textContent || '').startsWith('Artkeep：0 /')), '官方 PPD 下载转交')
const extensionWindow = await driver('/window/new', { type: 'tab' })
await driver('/window', { handle: extensionWindow.handle })
await driver('/url', { url: `chrome-extension://${extensionId}/tasks.html` })
const task = await script(async () => { const { read } = await import('./db.js'); return (await read('tasks')).filter(task => task.source === 'ppd' && task.status === 'awaitingDestination').sort((a,b) => b.createdAt.localeCompare(a.createdAt))[0] })
assert.ok(task, 'PPD 任务尚未持久化')
assert.equal(task.work.files.length, 5)
assert.equal(task.work.meta.title, meta.title)
assert.ok(task.work.files.every(file => file.path && preview.includes(file.path)))
assert.ok(task.work.files[0].path.includes(`/${meta.bmk}/`))
for (let index = 0; index < 5; index++) {
  const filename = entries.find(name => name.endsWith(`_p${index}.jpg`))
  const bytes = await fs.readFile(path.join(source, filename))
  await script(async (id, index, content) => { const { cacheFile } = await import('./db.js'); await cacheFile(id + '-' + index, new Blob([Uint8Array.from(atob(content), char => char.charCodeAt(0))], { type: 'image/jpeg' })); return true }, task.id, index, bytes.toString('base64'))
}
await script(async id => { const row = document.querySelector(`[data-id="${id}"]`); [...row.querySelectorAll('button')].find(button => button.textContent === 'Artkeep').click(); return true }, task.id)
await driver('/window', { handle: nativeWindow })
await cdp('Page.bringToFront')
const result = await until(() => script(async () => {
  const status = document.getElementById('artkeep-ppd-status')?.textContent
  if (status?.includes('保存完成')) return { status, complete: window.artkeepNativeComplete }
  if (status?.includes('反馈版本')) throw new Error(status)
  return false
}), 'Artkeep 保存完成与 PPD 完成记录')
assert.equal(result.complete, 1)
const records = await script(async id => {
  const db = await new Promise((resolve, reject) => { const request = indexedDB.open('DLRecord'); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error) })
  const tx = db.transaction('record' + id[0])
  const values = await Promise.all(Array.from({length:5}, (_, index) => new Promise((resolve, reject) => { const request = tx.objectStore('record' + id[0]).get(id + '_p' + index); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error) })))
  db.close(); return values
}, String(meta.idNum))
assert.ok(records.every(record => record?.n === task.work.files.find(file => file.id === record.id)?.path))
assert.equal(nativeMedia.length, 0, JSON.stringify(nativeMedia))
assert.equal(bookmarks.length, 1)
assert.equal(bookmarks[0].restrict, 1)
assert.deepEqual(bookmarks[0].tags, meta.tagsTranslOnly)
console.log('官方 PPD 19.6.1：原开始按钮转交 5 张图片、每次询问、实际上传、下载记录与完成回执通过；下载后收藏配置通过受控响应检查；PPD 媒体请求为 0')

async function importAgain(data, count) {
  await fs.writeFile(jsonFile, JSON.stringify([data])); chooser = null
  await script(async count => {
    const form = document.querySelector('form.settingForm')
    for (const [name, value] of [['bmkAfterDL', false], ['onlyCrawlFirstFewImagesSwitch', count < 5], ['onlyCrawlFirstFewImagesCount', count], ['autoStartDownload', true]]) {
      const input = form.elements.namedItem(name)
      if (input.type === 'checkbox') { if (input.checked !== value) input.click() }
      else { input.value = String(value); input.dispatchEvent(new Event('change', { bubbles: true })) }
    }
    return true
  }, count)
  await cdp('Runtime.evaluate', { expression: 'document.getElementById("importCrawlResults").click()', userGesture: true })
  await until(() => chooser, 'PPD 再次导入选择')
  await cdp('DOM.setFileInputFiles', { backendNodeId: chooser.backendNodeId, files: [jsonFile] })
  await until(() => script(async () => (document.getElementById('artkeep-ppd-status')?.textContent || '').startsWith('Artkeep：0 /')), 'PPD 自动开始转交')
  await driver('/window', { handle: extensionWindow.handle })
  await driver('/url', { url: `chrome-extension://${extensionId}/tasks.html` })
  const next = await script(async () => { const { read } = await import('./db.js'); return (await read('tasks')).filter(task => task.source === 'ppd' && task.status === 'awaitingDestination').sort((a,b) => b.createdAt.localeCompare(a.createdAt))[0] })
  assert.equal(next.work.files.length, count)
  return next
}
const auto = await importAgain(meta, 2)
for (let index = 0; index < 2; index++) {
  const bytes = await fs.readFile(path.join(source, entries.find(name => name.endsWith(`_p${index}.jpg`))))
  await script(async (id, index, content) => { const { cacheFile } = await import('./db.js'); await cacheFile(id + '-' + index, new Blob([Uint8Array.from(atob(content), char => char.charCodeAt(0))], { type: 'image/jpeg' })); return true }, auto.id, index, bytes.toString('base64'))
}
await script(async id => { const row = document.querySelector(`[data-id="${id}"]`); [...row.querySelectorAll('button')].find(button => button.textContent === 'Artkeep').click(); return true }, auto.id)
await driver('/window', { handle: nativeWindow }); await cdp('Page.bringToFront')
await until(() => script(async () => document.getElementById('artkeep-ppd-status')?.textContent === 'Artkeep：保存完成'), '自动开始保存完成')
assert.equal(await script(async () => window.artkeepNativeComplete), 2)
console.log('官方 PPD：自动开始与只抓取前 2 张图片通过')

const cancelMeta = { ...meta, idNum: 999999993, id: '999999993_p0', title: '取消检查' }
const cancelled = await importAgain(cancelMeta, 5)
await driver('/window', { handle: nativeWindow }); await cdp('Page.bringToFront')
await script(async () => { document.getElementById('pauseDownload').click(); return true })
await until(() => script(async () => document.getElementById('artkeep-ppd-status')?.textContent === 'Artkeep：已暂停'), 'PPD 暂停')
await driver('/refresh', {})
await until(() => script(async () => document.getElementById('artkeep-ppd-status')?.textContent === 'Artkeep：0 / 1'), '页面刷新后恢复原任务')
await script(async () => { document.getElementById('startDownload').click(); return true })
await until(() => script(async () => document.getElementById('artkeep-ppd-status')?.textContent === 'Artkeep：0 / 1'), 'PPD 继续')
await cdp('Runtime.evaluate', { expression: 'document.getElementById("stopDownload").click()', userGesture: true })
await until(() => script(async () => document.getElementById('artkeep-ppd-status')?.textContent === 'Artkeep：已取消'), 'PPD 取消')
await driver('/window', { handle: extensionWindow.handle })
const cancelledStatus = await script(async id => { const { read } = await import('./db.js'); return (await read('tasks', id)).status }, cancelled.id)
assert.equal(cancelledStatus, 'cancelled')
assert.equal(await script(async () => { const { read } = await import('./db.js'); return (await read('tasks')).filter(task => task.source === 'ppd' && task.work.sourceId === '999999993').length }), 1)
assert.equal(nativeMedia.length, 0, JSON.stringify(nativeMedia))
assert.equal(bookmarks.length, 1)
console.log('官方 PPD：原暂停／继续／取消按钮与页面刷新恢复通过；刷新后仍为同一任务')
await cdp('Fetch.disable'); socket.close()
