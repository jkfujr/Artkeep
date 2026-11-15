import { api } from './api.js'
import { read, updateTask, fileStore, cacheFile, cachedFile, removeFiles } from './db.js'
import { settings, pathFor, sidecars, mediaURL } from './shared.js'
import { ugoira } from './zip.js'

let running = false
const controllers = new Map()
const wait = ms => new Promise(resolve => setTimeout(resolve, ms))
async function background(message) { const r = await chrome.runtime.sendMessage(message); if (!r?.ok) throw new Error(r?.error || '扩展后台不可用'); return r.value }
async function checkpoint(id) { const t = await read('tasks', id); if (['paused', 'cancelled'].includes(t.status)) throw new Error(t.status); return t }
async function prepare(task, config) {
  if (task.files) return task
  const { work } = task
  const files = [...work.files].sort((a,b) => (a.index || 0) - (b.index || 0)).map((file, index) => {
    const id = file.id || `${work.sourceId}_${index}`
    const meta = work.platform === 'pixiv' ? work.meta : { idNum: work.sourceId, userId: work.meta.author.id, user: work.meta.author.nick || work.meta.author.name, title: work.meta.content.split('\n')[0].slice(0, 80) }
    const path = file.path || pathFor(work.platform === 'pixiv' ? config.naming : config.xNaming, { ...meta, id }) + '.' + file.ext
    return { ...file, path, index, cacheKey: `${task.id}-${index}`, local: 'pending' }
  })
  const stem = files[0].path.replace(/\.[^.]+$/, '').replace(/_p\d+$/, '').replace(/_\d+$/, work.platform === 'x' ? '' : '$&')
  const fileNames = files.map(file => {
    const from = stem.split('/').slice(0,-1), to = file.path.split('/')
    while (from.length && to.length && from[0] === to[0]) { from.shift(); to.shift() }
    return [...from.map(() => '..'), ...to].join('/')
  })
  const metas = work.platform === 'pixiv' ? sidecars({...work.meta, fileNames}, stem) : [
    { path: stem + '-meta.json', content: JSON.stringify(work.meta, null, 2), mime: 'application/json' },
    { path: stem + '-meta.txt', content: work.meta.content, mime: 'text/plain' },
  ]
  for (const file of metas) files.push({ ...file, index: files.length, cacheKey: `${task.id}-${files.length}`, local: 'pending' })
  const paths = new Set()
  for (const f of files) { const key = f.path.toLowerCase(); if (paths.has(key)) throw new Error('命名规则产生了重复文件名，请调整规则'); paths.add(key) }
  await updateTask(task.id, { files })
  return read('tasks', task.id)
}
async function download(task, file, signal) {
  let blob = await cachedFile(file.cacheKey)
  if (blob) return blob
  if (file.content !== undefined) { blob = new Blob([file.content], { type: file.mime }); await cacheFile(file.cacheKey, blob); return blob }
  const response = await fetch(mediaURL(file.url, task.work.platform), { signal, credentials: 'omit', redirect: 'error' })
  if (!response.ok || !response.body) throw new Error(`媒体下载失败（${response.status}），请重新打开来源页面`)
  const dir = await fileStore(), temporary = await dir.getFileHandle(file.cacheKey + '-partial', { create: true }), output = await temporary.createWritable()
  try {
    await response.body.pipeTo(output, { signal })
    blob = await temporary.getFile()
    if (file.ext === 'ugoira') {
      const m = task.work.meta, info = m.ugoiraInfo
      if (!info?.frames?.length) throw new Error('动图缺少帧时间信息')
      blob = await ugoira(blob, { frames: info.frames, mime_type: info.mime_type, id: task.work.sourceId, title: m.title, tags: m.tags, date: m.date, xRestrict: m.xRestrict, width: m.fullWidth, height: m.fullHeight, user: m.user, userId: m.userId })
    }
    await cacheFile(file.cacheKey, blob)
  } finally { await dir.removeEntry(file.cacheKey + '-partial').catch(() => {}) }
  return blob
}
async function localSave(task, index, blob) {
  task = await checkpoint(task.id)
  if (task.files[index].local === 'completed') return
  const file = task.files[index]
  const mime = file.mime || ({ jpg:'image/jpeg', jpeg:'image/jpeg', png:'image/png', gif:'image/gif', webp:'image/webp', mp4:'video/mp4', webm:'video/webm', ugoira:'application/octet-stream' })[file.ext] || 'application/octet-stream'
  const url = URL.createObjectURL(new Blob([blob], { type: mime }))
  try {
    const { id, complete } = await background({ type: 'saveLocal', id: task.id, index, path: file.path, url })
    if (!complete) {
      while (true) {
        await checkpoint(task.id)
        const [item] = await background({ type: 'downloadStatus', id })
        if (!item || item.state === 'interrupted') throw new Error('本地保存失败，请重试')
        if (item.state === 'complete') break
        await wait(500)
      }
    }
    const fresh = await read('tasks', task.id); fresh.files[index].local = 'completed'; await updateTask(task.id, { files: fresh.files })
  } finally { URL.revokeObjectURL(url) }
}
async function upload(task, config, signal) {
  if (task.upload === 'completed') return
  if (!task.uploadId) {
    const files = []
    for (const file of task.files) { const blob = await cachedFile(file.cacheKey); files.push({ path: file.path, size: blob.size, modified: 0 }) }
    const value = await api(config, '/uploads', { method: 'POST', key: task.requestId, body: { name: `${task.work.platform}-${task.work.sourceId}`, files }, signal })
    await updateTask(task.id, { uploadId: value.id, server: config.server }); task = await checkpoint(task.id)
  }
  if (task.server !== config.server) throw new Error('此任务属于另一台 Artkeep 服务器，请恢复原服务器设置后继续')
  const session = await api(config, `/uploads/${task.uploadId}`, { signal })
  if (session.status === 'cancelled') throw new Error('服务端上传已取消，请重新收藏')
  if (!task.jobId && session.jobId) { await updateTask(task.id, { jobId: session.jobId }); task = await checkpoint(task.id) }
  if (session.status === 'uploading') {
    for (let i = 0; i < task.files.length; i++) {
      const blob = await cachedFile(task.files[i].cacheKey)
      let offset = session.files[i].offset
      while (offset < blob.size) {
        await checkpoint(task.id)
        const chunk = blob.slice(offset, offset + session.chunkSize)
        try { await api(config, `/uploads/${task.uploadId}/files/${i}/chunks/${offset}`, { method: 'PUT', body: chunk, signal }); offset += chunk.size }
        catch (error) { const saved = await api(config, `/uploads/${task.uploadId}/files/${i}`, { signal }); if (saved.offset <= offset) throw error; offset = saved.offset }
        await updateTask(task.id, { phase: 'uploading', fileIndex: i, fileOffset: offset })
      }
    }
    const job = await api(config, '/jobs', { method: 'POST', key: `${task.requestId}-import`, body: { kind: 'import', source: 'upload', uploadId: task.uploadId }, signal })
    await updateTask(task.id, { jobId: job.id }); task = await checkpoint(task.id)
  }
  if (!task.jobId) throw new Error('服务端未返回导入任务，请重试查询')
  let job = await api(config, `/jobs/${task.jobId}`, { signal })
  if (['failed', 'interrupted', 'partial'].includes(job.status)) {
    task.retryKey ||= crypto.randomUUID(); await updateTask(task.id, { retryKey: task.retryKey })
    job = await api(config, `/jobs/${task.jobId}`, { method: 'PATCH', key: task.retryKey, body: { status: 'queued' }, signal })
    await updateTask(task.id, { retryKey: null })
  }
  while (true) {
    await checkpoint(task.id); await updateTask(task.id, { phase: 'importing' })
    if (job.status === 'completed') break
    if (['failed', 'interrupted', 'partial'].includes(job.status)) throw new Error(job.error || '作品导入失败，请重试')
    await wait(1500); job = await api(config, `/jobs/${task.jobId}`, { signal })
  }
  const result = await api(config, `/sources/${task.work.platform}/${task.work.platform === 'pixiv' ? 'artwork' : 'tweet'}/${task.work.sourceId}`, { signal })
  if (!result.collected) throw new Error('导入任务完成，来源映射缺失，请检查服务端')
  await updateTask(task.id, { upload: 'completed', workId: result.workId })
}
async function process(task) {
  const controller = new AbortController(); controllers.set(task.id, controller)
  const timer = setInterval(async () => { const t = await read('tasks', task.id); if (['paused', 'cancelled'].includes(t.status)) controller.abort() }, 500)
  try {
    const config = await settings()
    if (!['local', 'artkeep', 'both'].includes(task.destination)) throw new Error('请选择保存位置')
    await updateTask(task.id, { status: 'running', error: '' }); task = await prepare(task, config)
    const failures = []
    try {
      for (let i = 0; i < task.files.length; i++) {
        await checkpoint(task.id); await updateTask(task.id, { phase: 'downloading', fileIndex: i })
        const blob = await download(task, task.files[i], controller.signal)
        if (['local', 'both'].includes(task.destination)) { try { await localSave(task, i, blob) } catch (e) { if (controller.signal.aborted) throw e; failures.push(e.message) } }
      }
      const fresh = await checkpoint(task.id)
      if (['local', 'both'].includes(task.destination) && fresh.files.every(f => f.local === 'completed')) await updateTask(task.id, { local: 'completed' })
    } catch (e) { if (controller.signal.aborted) throw e; failures.push(e.message) }
    task = await checkpoint(task.id)
    if (['artkeep', 'both'].includes(task.destination)) {
      try { if (!(await Promise.all(task.files.map(f => cachedFile(f.cacheKey)))).every(Boolean)) throw new Error('媒体尚未下载完成'); await upload(task, config, controller.signal) } catch (e) { if (controller.signal.aborted) throw e; failures.push(e.message) }
    }
    if (failures.length) throw new Error([...new Set(failures)].join('；'))
    task = await checkpoint(task.id)
    if (task.bookmarkAfterSave && task.bookmark !== 'completed') await background({ type: 'bookmark', id: task.id })
    await updateTask(task.id, { status: 'completed', phase: 'completed', error: '' })
    await removeFiles(task)
  } catch (error) {
    const fresh = await read('tasks', task.id)
    if (!['paused', 'cancelled'].includes(fresh.status)) await updateTask(task.id, { status: 'failed', error: error.message })
  } finally { clearInterval(timer); controllers.delete(task.id) }
}
async function run() {
  if (running) return
  running = true
  try {
    const config = await settings(), count = Math.max(1, Math.min(3, Number(config.downloadThread) || 3))
    const worker = async () => {
      while (true) {
        const next = (await read('tasks')).find(t => t.status === 'queued' && !controllers.has(t.id))
        if (!next) break
        // 在任何异步处理前标记，避免同一批并发选中同一任务。
        controllers.set(next.id, null)
        await process(next)
        await wait(Math.max(0, Number(config.downloadInterval) || 0) * 1000)
      }
    }
    await Promise.all(Array.from({ length: count }, worker))
    for (const task of await read('tasks')) if (task.status === 'cancelled') {
      if (task.uploadId && !task.jobId) await api(config, `/uploads/${task.uploadId}`, { method: 'DELETE' }).catch(() => {})
      await removeFiles(task)
    }
  } finally { running = false }
}
chrome.runtime.onMessage.addListener((message, _sender, reply) => {
  if (message.target !== 'engine') return false
  if (message.type === 'run') { run().catch(console.error); reply({ ok: true }); return false }
})
for (const t of await read('tasks')) if (t.status === 'running') await updateTask(t.id, { status: 'queued' })
run().catch(console.error)
