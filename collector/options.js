import { settings, pathFor } from './shared.js'
import { api } from './api.js'
const form = document.querySelector('#settings'), result = document.querySelector('#result')
const fields = ['server', 'token', 'destination', 'naming', 'xNaming', 'takeover', 'bookmarkAfterSave']
let current = await settings()
function show(message, error = false) { result.textContent = message; result.className = error ? 'error' : '' }
function render() { for (const key of fields) { const input = document.getElementById(key); input.type === 'checkbox' ? input.checked = current[key] : input.value = current[key] } }
function values() {
  const next = { ...current }
  for (const key of fields) { const input = document.getElementById(key); next[key] = input.type === 'checkbox' ? input.checked : input.value.trim() }
  return next
}
async function permit(server) {
  if (!server) return
  const url = new URL(server)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('服务器地址无效')
  if (!await chrome.permissions.request({ origins: [url.origin + '/*'] })) throw new Error('请允许访问此服务器')
}
document.getElementById('origin').value = 'chrome-extension://' + chrome.runtime.id
render()
form.onsubmit = async event => {
  event.preventDefault()
  try {
    const next = values()
    pathFor(next.naming, { userId: '1', user: '作者', idNum: '2', title: '作品', id: '2_p0' }); pathFor(next.xNaming, { userId: '1', user: '作者', idNum: '2', title: '作品', id: '2_0' })
    await permit(next.server); await chrome.storage.local.set({ settings: next }); current = next; show('设置已保存')
  } catch (e) { show(e.message, true) }
}
document.getElementById('copy-origin').onclick = () => navigator.clipboard.writeText('chrome-extension://' + chrome.runtime.id).then(() => show('扩展来源已复制'), e => show(e.message, true))
document.getElementById('test').onclick = async () => { try { const config = values(); await permit(config.server); const value = await api(config, '/capabilities'); if (value.apiVersion !== 'v1') throw new Error('服务器 API 版本不兼容'); await api(config, '/jobs?pageSize=1'); show('连接成功') } catch (e) { show(e.message, true) } }
document.getElementById('import').onchange = async event => {
  try {
    const file = event.target.files[0]; if (!file) return
    const data = JSON.parse(await file.text())
    if (typeof data.userSetName !== 'string') throw new Error('PPD 配置格式无效')
    current = { ...values(), naming: data.userSetName, bookmarkAfterSave: data.bmkAfterDL === true, bookmarkRestrict: data.restrictBoolean === true, downloadThread: Math.max(1, Math.min(3, Number(data.downloadThread) || 3)), downloadInterval: data.downloadIntervalSwitch ? Number(data.downloadInterval) || 1 : 0 }
    render(); show('配置已读取，保存设置后生效')
  } catch (e) { show(e.message, true) }
}
