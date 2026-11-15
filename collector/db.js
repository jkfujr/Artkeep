let opening
export function database() {
  return opening ||= new Promise((resolve, reject) => {
    const request = indexedDB.open('artkeep-collector', 1)
    request.onupgradeneeded = () => {
      request.result.createObjectStore('tasks', { keyPath: 'id' })
      request.result.createObjectStore('requests', { keyPath: 'id' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}
export async function read(store, id) {
  const db = await database()
  return new Promise((resolve, reject) => {
    const req = db.transaction(store).objectStore(store)[id === undefined ? 'getAll' : 'get'](id)
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}
export async function write(store, value) {
  const db = await database()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(store, 'readwrite')
    tx.objectStore(store).put(value)
    tx.oncomplete = () => resolve(value)
    tx.onerror = () => reject(tx.error)
  })
}
export async function updateTask(id, change) {
  const db = await database()
  return new Promise((resolve, reject) => {
    const tx = db.transaction('tasks', 'readwrite'), store = tx.objectStore('tasks')
    let next
    const req = store.get(id)
    req.onsuccess = () => { next = { ...req.result, ...change }; store.put(next) }
    tx.oncomplete = () => resolve(next)
    tx.onerror = () => reject(tx.error)
  })
}
export async function saveBatch(batch, tasks) {
  const db = await database()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(['tasks', 'requests'], 'readwrite'), requests = tx.objectStore('requests')
    const request = requests.get(batch.id)
    let value = batch, conflict = false
    request.onsuccess = () => {
      if (request.result) {
        value = request.result
        if (value.signature !== batch.signature) { conflict = true; tx.abort() }
        return
      }
      for (const task of tasks) tx.objectStore('tasks').put(task)
      requests.put(batch)
    }
    tx.oncomplete = () => resolve(value)
    tx.onabort = () => reject(conflict ? new Error('同一任务标识对应了不同作品') : tx.error)
    tx.onerror = () => reject(tx.error)
  })
}
export async function fileStore() { return (await navigator.storage.getDirectory()).getDirectoryHandle('media', { create: true }) }
export async function cacheFile(key, blob) {
  const dir = await fileStore(), handle = await dir.getFileHandle(key, { create: true }), out = await handle.createWritable()
  await out.write(blob); await out.close()
}
export async function cachedFile(key) {
  try { return await (await (await fileStore()).getFileHandle(key)).getFile() } catch (error) { if (error.name === 'NotFoundError') return null; throw error }
}
export async function removeFiles(task) {
  const dir = await fileStore()
  for (let i = 0; i < (task.files?.length || 0); i++) await dir.removeEntry(`${task.id}-${i}`).catch(error => { if (error.name !== 'NotFoundError') throw error })
}
