const root = document.getElementById('tasks'), error = document.getElementById('error')
const labels = { queued: '排队中', running: '处理中', paused: '已暂停', failed: '失败', cancelled: '已取消', completed: '已完成', awaitingDestination: '请选择保存位置', pending: '待处理', downloading: '正在下载', uploading: '正在上传', importing: '正在入库' }
function text(tag, value, className) { const e = document.createElement(tag); e.textContent = value; if (className) e.className = className; return e }
async function send(message) { const r = await chrome.runtime.sendMessage(message); if (!r?.ok) throw new Error(r?.error || '扩展后台不可用'); return r.value }
let signature = '', loading = false
async function refresh() {
  if (loading) return
  loading = true
  try {
    const tasks = (await send({ type: 'list' })).sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    const next = JSON.stringify(tasks)
    if (next === signature) return
    signature = next; root.replaceChildren()
    if (!tasks.length) { root.append(text('p', '暂无收藏任务', 'empty')); return }
    for (const task of tasks) {
      const row = document.createElement('section'); row.className = 'task'; row.dataset.id = task.id
      row.append(text('h2', task.work.meta.title || task.work.meta.content?.split('\n')[0] || task.work.sourceId), text('p', (task.work.platform === 'pixiv' ? 'Pixiv' : 'X') + ' · ' + task.work.sourceId + ' · ' + (labels[task.status] || task.status), 'state'))
      if (task.status === 'running') row.append(text('p', (labels[task.phase] || '准备中') + (task.files ? ' · ' + ((task.fileIndex || 0) + 1) + ' / ' + task.files.length : ''), 'muted'))
      row.append(text('p', '本地：' + (['local', 'both'].includes(task.destination) ? labels[task.local] : '未选择') + '　Artkeep：' + (['artkeep', 'both'].includes(task.destination) ? labels[task.upload] : '未选择'), 'state'))
      if (task.error) row.append(text('p', task.error, 'error'))
      const actions = document.createElement('div'); actions.className = 'actions'
      function button(label, action, destination) {
        const b = text('button', label); b.onclick = async () => {
          b.disabled = true; error.textContent = ''
          try {
            const ids = action === 'destination' ? tasks.filter(t => t.batch === task.batch && t.status === 'awaitingDestination').map(t => t.id) : [task.id]
            await send({ type: 'control', ids, action, destination }); signature = ''; await refresh()
          } catch (e) { error.textContent = e.message } finally { b.disabled = false }
        }; actions.append(b)
      }
      if (task.status === 'awaitingDestination') { button('Artkeep', 'destination', 'artkeep'); button('本地', 'destination', 'local'); button('本地与 Artkeep', 'destination', 'both') }
      if (['running', 'queued'].includes(task.status)) button('暂停', 'pause')
      if (['failed', 'paused'].includes(task.status)) button(task.status === 'failed' ? '重试' : '继续', 'resume')
      if (!['completed', 'cancelled'].includes(task.status)) button('取消', 'cancel')
      if (task.workId && task.server) { const a = text('a', '打开收藏'); a.className = 'button'; a.href = task.server.replace(/\/$/, '') + '/works/' + encodeURIComponent(task.workId); a.target = '_blank'; a.rel = 'noreferrer'; actions.append(a) }
      row.append(actions); root.append(row)
    }
  } catch (e) { error.textContent = e.message } finally { loading = false }
}
document.getElementById('refresh').onclick = refresh
refresh(); setInterval(refresh, 2000)
