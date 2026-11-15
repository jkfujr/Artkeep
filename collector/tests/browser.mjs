import fs from 'node:fs/promises'
import path from 'node:path'
import assert from 'node:assert/strict'
const root = process.cwd(), session = JSON.parse(await fs.readFile(path.join(root, 'artifacts/webdriver/session.json'), 'utf8')).id
const base = 'http://127.0.0.1:9517/session/' + session, server = process.env.ARTKEEP_TEST_SERVER || 'http://127.0.0.1:28080'
const extensionId = (await fs.readFile('collector/extension-id.txt', 'utf8')).trim()
const source = process.argv[2]
if (!source) throw new Error('请提供实际样例作品目录')
async function driver(endpoint, data = {}, method = 'POST') {
  const response = await fetch(base + endpoint, { method, headers: { 'Content-Type': 'application/json' }, ...(method === 'GET' ? {} : { body: JSON.stringify(data) }) })
  const result = await response.json()
  if (!response.ok) throw new Error(result.value?.message || JSON.stringify(result))
  return result.value
}
async function script(fn, ...args) {
  const result = await driver('/execute/async', { script: 'const done=arguments[arguments.length-1];(' + fn.toString() + ')(...Array.from(arguments).slice(0,-1)).then(value=>done({ok:true,value}),error=>done({ok:false,error:error.stack}));', args })
  if (!result.ok) throw new Error(result.error)
  return result.value
}
await driver('/timeouts', { script: 60000 })
await driver('/url', { url: 'chrome-extension://' + extensionId + '/options.html' })
const login = await fetch(server + '/api/v1/sessions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ scope: 'admin', password: process.env.ARTKEEP_TEST_PASSWORD || 'artkeep-validation-only' }) })
assert.equal(login.status, 201)
const cookie = login.headers.get('set-cookie').split(';')[0]
const issued = await fetch(server + '/api/v1/access-tokens', { method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie }, body: JSON.stringify({ name: '隔离浏览器验收', origin: 'chrome-extension://' + extensionId }) })
assert.equal(issued.status, 201)
const token = (await issued.json()).token
await script(async (server, token) => { await chrome.storage.local.set({ settings: { server, token, destination: 'both', takeover: false, downloadThread: 1, downloadInterval: 0 } }); return true }, server, token)
const files = (await fs.readdir(source)).filter(file => /\.jpg$/i.test(file)).sort()
const metadata = JSON.parse(await fs.readFile(path.join(source, (await fs.readdir(source)).find(file => file.endsWith('-meta.json'))), 'utf8'))
assert.equal(files.length, 5)
for (const destination of ['both', 'local', 'artkeep']) {
  const id = crypto.randomUUID()
  await script(async (id, destination, meta) => {
    const { write } = await import('./db.js')
    const work = { platform: 'pixiv', sourceId: String(meta.idNum), meta, files: [] }
    for (let index = 0; index < 5; index++) work.files.push({ url: meta.original.replace('_p0.', '_p' + index + '.'), ext: 'jpg', id: String(meta.idNum) + '_p' + index, index })
    await write('tasks', { id, work, destination, source: 'test', status: 'paused', local: 'pending', upload: 'pending', requestId: crypto.randomUUID(), createdAt: new Date().toISOString() })
    return true
  }, id, destination, metadata)
  for (let i = 0; i < files.length; i++) {
    const bytes = await fs.readFile(path.join(source, files[i]))
    await script(async (id, index, data) => {
      const { cacheFile } = await import('./db.js')
      const bytes = Uint8Array.from(atob(data), c => c.charCodeAt(0))
      await cacheFile(id + '-' + index, new Blob([bytes], {type:'image/jpeg'}))
      return true
    }, id, i, bytes.toString('base64'))
  }
  await script(async id => { const r = await chrome.runtime.sendMessage({ type: 'control', ids: [id], action: 'resume' }); if (!r.ok) throw new Error(r.error); return true }, id)
  let task
  for (let n = 0; n < 100; n++) {
    task = await script(async id => { const { read } = await import('./db.js'); const t = await read('tasks', id); return {status:t.status,error:t.error,phase:t.phase,local:t.local,upload:t.upload,workId:t.workId,files:t.files?.length} }, id)
    if (['completed', 'failed'].includes(task.status)) break
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  assert.equal(task.status, 'completed', JSON.stringify(task))
  assert.equal(task.files, 8)
  assert.equal(task.local, destination === 'artkeep' ? 'pending' : 'completed')
  assert.equal(task.upload, destination === 'local' ? 'pending' : 'completed')
  if (destination !== 'artkeep') await script(async id => {
    const {read} = await import('./db.js'), task = await read('tasks', id)
    for (const file of task.files) {
      const [download] = await chrome.downloads.search({id:file.downloadId})
      if (!download || !download.filename.replaceAll('\\','/').endsWith('/'+file.path)) throw new Error('本地文件名改变：' + download?.filename)
    }
    return true
  }, id)
  console.log(JSON.stringify({ destination, ...task }))
}
const found = await fetch(server + '/api/v1/works?includeAdult=true', {headers:{Cookie:cookie}})
const works = await found.json()
assert.equal(works.total, 1)
assert.equal(works.items[0].mediaCount, 5)
assert.equal(works.items[0].title, metadata.title)
assert.equal(works.items[0].authorId, metadata.userId)
assert.equal(works.items[0].rating, 1)
await driver('/url', {url:'chrome-extension://' + extensionId + '/tasks.html'})
const shot = await driver('/screenshot', {}, 'GET')
await fs.writeFile('artifacts/collector-tasks.png', Buffer.from(shot, 'base64'))
console.log('真实样例：1 部作品、5 张图片、3 份附属文件，三种保存去向通过')
