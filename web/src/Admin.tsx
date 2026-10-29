import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Button, Switch } from '@heroui/react'
import { ArrowUpRight, Check, Clock3, Download, FolderOpen, Link2, LogOut, RefreshCw, ShieldCheck } from 'lucide-react'
import { api, dateLabel, message, mutate } from './api'
import type { Job, Settings } from './api'
import { ErrorNotice, Field } from './ui'
import { useApp } from './App'

const statusLabels: Record<string, string> = { queued: '排队中', running: '处理中', completed: '已完成', failed: '失败', interrupted: '已中断' }

export default function Admin() {
  const { site, refreshSite, invalidate } = useApp()
  const [password, setPassword] = useState('')
  const [loginError, setLoginError] = useState('')
  const [busy, setBusy] = useState('')
  const [settings, setSettings] = useState<Settings | null>(null)
  const [jobs, setJobs] = useState<Job[]>([])
  const [url, setUrl] = useState('')
  const [error, setError] = useState('')
  const [result, setResult] = useState('')
  const [existingWork, setExistingWork] = useState('')
  const [required, setRequired] = useState(false)
  const [adultPassword, setAdultPassword] = useState('')
  const [settingsError, setSettingsError] = useState('')
  const [settingsSaved, setSettingsSaved] = useState(false)
  const statuses = useRef(new Map<string, string>())
  const fetchJobs = useCallback(async () => { const next = await api<Job[]>('/api/admin/jobs'); if (next.some(job => job.status === 'completed' && statuses.current.has(job.id) && statuses.current.get(job.id) !== 'completed')) invalidate(); statuses.current = new Map(next.map(job => [job.id, job.status])); setJobs(next) }, [invalidate])
  const fetchSettings = useCallback(async () => { const next = await api<Settings>('/api/admin/settings'); setSettings(next); setRequired(next.adultPasswordRequired) }, [])
  useEffect(() => { window.scrollTo(0, 0); if (site?.admin) { Promise.all([fetchJobs(), fetchSettings()]).catch(error => setError(message(error))) } }, [site?.admin, fetchJobs, fetchSettings])
  const active = jobs.some(job => job.status === 'queued' || job.status === 'running')
  useEffect(() => { if (!site?.admin || !active) return; const timer = window.setInterval(() => fetchJobs().catch(error => setError(message(error))), 2000); return () => clearInterval(timer) }, [site?.admin, active, fetchJobs])
  const login = async () => { if (busy) return; setBusy('login'); setLoginError(''); try { await mutate('/api/admin/session', 'POST', { password }); setPassword(''); await refreshSite() } catch (error) { setLoginError(message(error)) } finally { setBusy('') } }
  const logout = async () => { try { await mutate('/api/admin/session', 'DELETE'); await refreshSite(); invalidate() } catch (error) { setError(message(error)) } }
  const importWork = async (kind: string) => { if (busy) return; setBusy(kind); setError(''); setResult(''); setExistingWork(''); try { const response = await mutate<Job | { existing: boolean; workId: string }>('/api/admin/imports', 'POST', { kind, ...(kind === 'x' ? { url } : {}) }); if ('existing' in response) { setResult('已收藏'); setExistingWork(response.workId) } else { setResult('已加入任务'); await fetchJobs() } if (kind === 'x') setUrl('') } catch (error) { setError(message(error)) } finally { setBusy('') } }
  const retry = async (id: string) => { setBusy(id); setError(''); try { await mutate(`/api/admin/jobs/${id}/retry`, 'POST'); await fetchJobs() } catch (error) { setError(message(error)) } finally { setBusy('') } }
  const saveSettings = async () => { if (busy) return; setBusy('settings'); setSettingsError(''); setSettingsSaved(false); try { const next = await mutate<Settings>('/api/admin/settings', 'PATCH', { adultPasswordRequired: required, adultPassword }); setSettings(next); setAdultPassword(''); setSettingsSaved(true); await refreshSite(); invalidate() } catch (error) { setSettingsError(message(error)) } finally { setBusy('') } }

  if (!site) return <main className="page-shell"><div className="loading-line" /></main>
  if (!site.admin) return <main className="login-page"><div className="login-symbol"><ShieldCheck size={28} strokeWidth={1.5} /></div><h1>管理员登录</h1><form onSubmit={event => { event.preventDefault(); login() }}><Field label="管理员密码" value={password} onChange={setPassword} type="password" autoFocus required /><ErrorNotice>{loginError}</ErrorNotice><Button type="submit" fullWidth isDisabled={!!busy}>{busy === 'login' ? '正在登录' : '登录'}</Button></form><Link to="/" className="back-link">返回图册</Link></main>

  return <main className="page-shell admin-page"><div className="page-heading"><h1>管理收藏</h1><Button variant="ghost" onPress={logout}><LogOut size={16} />退出登录</Button></div><div className="admin-grid" id="add"><section className="admin-panel"><div className="panel-title"><span className="panel-icon"><Link2 size={20} /></span><h2>收藏 X 作品</h2></div><form onSubmit={event => { event.preventDefault(); importWork('x') }}><Field label="帖子链接" type="url" value={url} onChange={setUrl} placeholder="https://x.com/…/status/…" required /><Button type="submit" isDisabled={!!busy || !url.trim()}><Download size={16} />{busy === 'x' ? '正在添加' : '下载收藏'}</Button></form>{result && <div className="success-notice" role="status"><Check size={15} />{result}{existingWork && <Link to={`/works/${existingWork}`}>查看作品<ArrowUpRight size={13} /></Link>}</div>}</section>
    <section className="admin-panel"><div className="panel-title"><span className="panel-icon"><FolderOpen size={20} /></span><h2>Pixiv 收藏</h2></div><div className="field"><label>收藏目录</label><div className="directory-value">{settings?.pixivDirectory || '尚未配置'}</div></div><Button variant="secondary" onPress={() => importWork('pixiv')} isDisabled={!!busy || jobs.some(job => job.kind === 'pixiv' && ['running', 'queued'].includes(job.status))}><RefreshCw size={16} />{busy === 'pixiv' ? '正在添加' : '扫描目录'}</Button></section></div><ErrorNotice>{error}</ErrorNotice>
    <section className="admin-panel jobs-panel"><div className="panel-title"><span className="panel-icon"><Clock3 size={20} /></span><h2>导入任务</h2><Button className="panel-refresh" variant="ghost" isIconOnly aria-label="刷新任务" onPress={() => fetchJobs().catch(error => setError(message(error)))}><RefreshCw size={16} /></Button></div>{!jobs.length ? <div className="panel-empty">暂无任务</div> : <div className="jobs-list">{jobs.map(job => <div className="job-row" key={job.id}><div className={`job-indicator status-${job.status}`}>{job.status === 'completed' ? <Check size={17} /> : job.status === 'running' ? <RefreshCw size={16} className="spin" /> : <Download size={16} />}</div><div className="job-main"><div className="job-name">{job.kind === 'pixiv' ? 'Pixiv 目录扫描' : job.input}</div><div className="job-meta"><span className={`job-status status-${job.status}`}>{statusLabels[job.status] || job.status}</span>{job.total > 0 && <span>{job.progress.toLocaleString()} / {job.total.toLocaleString()}</span>}<time dateTime={job.createdAt}>{dateLabel(job.createdAt)}</time></div>{job.error && <div className="job-error">{job.error}</div>}{job.status === 'running' && job.total > 0 && <progress aria-label="任务进度" max={job.total} value={job.progress} />}</div>{job.workId && job.status === 'completed' && <Link to={`/works/${job.workId}`} className="job-open" aria-label="查看作品"><ArrowUpRight size={18} /></Link>}{['failed', 'interrupted'].includes(job.status) && <Button size="sm" variant="secondary" onPress={() => retry(job.id)} isDisabled={!!busy}>重试</Button>}</div>)}</div>}</section>
    <section className="admin-panel settings-panel"><div className="panel-title"><span className="panel-icon"><ShieldCheck size={20} /></span><h2>R18 浏览设置</h2></div><div className="settings-form"><Switch isSelected={required} onChange={value => { setRequired(value); setSettingsSaved(false) }}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control>开启 R18 需要密码</Switch.Content></Switch><Field label={settings?.adultPasswordSet ? '新密码（选填）' : 'R18 浏览密码'} value={adultPassword} onChange={value => { setAdultPassword(value); setSettingsSaved(false) }} type="password" /><ErrorNotice>{settingsError}</ErrorNotice><div className="settings-submit"><Button onPress={saveSettings} isDisabled={!!busy}>{busy === 'settings' ? '正在保存' : '保存设置'}</Button>{settingsSaved && <span className="success-notice" role="status"><Check size={15} />已保存</span>}</div></div></section>
  </main>
}
