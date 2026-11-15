const response = await chrome.runtime.sendMessage({ type: 'list' })
const tasks = response?.value || [], active = tasks.filter(t => ['queued', 'running'].includes(t.status)).length, failed = tasks.filter(t => t.status === 'failed').length
document.getElementById('count').textContent = '处理中 ' + active + ' · 失败 ' + failed
