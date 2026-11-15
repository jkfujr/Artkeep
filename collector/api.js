function serverURL(config, path) {
  const url = new URL(config.server)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('请配置有效的 Artkeep 服务器地址')
  return url.href.replace(/\/$/, '') + '/api/v1' + path
}
export async function api(config, path, { key, method = 'GET', body, signal } = {}) {
  if (!config.token) throw new Error('请配置 Artkeep 访问令牌')
  const binary = body instanceof Blob
  const response = await fetch(serverURL(config, path), { method, signal, credentials: 'omit', redirect: 'error', headers: { Authorization: `Bearer ${config.token}`, ...(key ? { 'Idempotency-Key': key } : {}), ...(body === undefined ? {} : { 'Content-Type': binary ? 'application/octet-stream' : 'application/json' }) }, body: body === undefined ? undefined : binary ? body : JSON.stringify(body) })
  if (!response.ok) { const problem = await response.json().catch(() => ({})); throw new Error(problem.detail || `Artkeep 返回 ${response.status}`) }
  return response.status === 204 ? null : response.json()
}
