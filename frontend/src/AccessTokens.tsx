import { useEffect, useState } from 'react'
import { Button } from '@heroui/react'
import { KeyRound } from 'lucide-react'
import { api, mutate, message } from './api'
import { ErrorNotice, Field } from './ui'

interface Token { id: string; name: string; origin: string; createdAt: string; token?: string }

export default function AccessTokens() {
  const [items, setItems] = useState<Token[]>([])
  const [name, setName] = useState('浏览器收藏插件')
  const [origin, setOrigin] = useState('')
  const [created, setCreated] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const load = async () => setItems((await api<{ items: Token[] }>('/api/v1/access-tokens')).items)
  useEffect(() => { load().catch(error => setError(message(error))) }, [])
  const create = async () => {
    setBusy(true); setError(''); setCreated('')
    try { const value = await mutate<Token>('/api/v1/access-tokens', 'POST', { name, origin }); setCreated(value.token || ''); await load() } catch (error) { setError(message(error)) } finally { setBusy(false) }
  }
  const revoke = async (id: string) => {
    setBusy(true); setError('')
    try { await mutate(`/api/v1/access-tokens/${id}`, 'DELETE'); setCreated(''); await load() } catch (error) { setError(message(error)) } finally { setBusy(false) }
  }
  return <section className="admin-panel settings-panel"><div className="panel-title"><span className="panel-icon"><KeyRound size={20} /></span><h2>访问令牌</h2></div>
    <form className="settings-form" onSubmit={event => { event.preventDefault(); create() }}>
      <Field label="名称" value={name} onChange={setName} required />
      <Field label="扩展来源" value={origin} onChange={setOrigin} placeholder="chrome-extension://扩展ID" required />
      <Button type="submit" isDisabled={busy}>{busy ? '正在处理' : '创建令牌'}</Button>
      {created && <div className="field"><label htmlFor="created-token">访问令牌（关闭后隐藏）</label><input id="created-token" readOnly value={created} onFocus={event => event.target.select()} /><Button variant="secondary" onPress={() => navigator.clipboard.writeText(created).catch(error => setError(message(error)))}>复制令牌</Button></div>}
      <ErrorNotice>{error}</ErrorNotice>
    </form>
    <div className="jobs-list">{items.map(item => <div className="job-row" key={item.id}><div className="job-content"><strong>{item.name}</strong><div style={{ overflowWrap: 'anywhere' }}>{item.origin}</div></div><Button variant="ghost" isDisabled={busy} onPress={() => revoke(item.id)}>撤销</Button></div>)}</div>
  </section>
}
