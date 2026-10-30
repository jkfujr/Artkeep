import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Button, Switch } from '@heroui/react'
import { ArrowUpRight, Check, Clock3, Download, LogOut, RefreshCw, ShieldCheck } from 'lucide-react'
import { api, dateLabel, message, mutate } from './api'
import type { Job, Settings } from './api'
import { ErrorNotice, Field } from './ui'
import { useApp } from './App'
import ImportPanel from './ImportPanel'

const statusLabels: Record<string, string> = { queued: '排队中', running: '处理中', completed: '已完成', failed: '失败', interrupted: '已中断', partial: '部分完成', existing: '已存在' }

function jobName(job: Job) {
  if (job.kind !== 'batch') return job.kind === 'pixiv' ? 'Pixiv 目录扫描' : job.input
  try { const input = JSON.parse(job.input); return input.source === 'migration' ? '迁移已有收藏' : input.source === 'url' ? 'URL 导入（' + (input.urls?.length || 0) + '）' : input.source === 'server' ? input.path : '本机文件夹导入' } catch { return '导入' }
}

export default function Admin() {
  const { site, refreshSite, invalidate } = useApp()
  const [password, setPassword] = useState('')
  const [loginError, setLoginError] = useState('')
  const [busy, setBusy] = useState('')
  const [settings, setSettings] = useState<Settings | null>(null)
  const [jobs, setJobs] = useState<Job[]>([])
  const [error, setError] = useState('')
  const [required, setRequired] = useState(false)
  const [adultPassword, setAdultPassword] = useState('')
  const [settingsError, setSettingsError] = useState('')
  const [settingsSaved, setSettingsSaved] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [passwordSaved, setPasswordSaved] = useState(false)
  const statuses = useRef(new Map<string, string>())
  const fetchJobs = useCallback(async () => { const next = await api<Job[]>('/api/admin/jobs'); if (next.some(job => ['completed', 'partial'].includes(job.status) && statuses.current.has(job.id) && statuses.current.get(job.id) !== job.status)) invalidate(); statuses.current = new Map(next.map(job => [job.id, job.status])); setJobs(next) }, [invalidate])
  const fetchSettings = useCallback(async () => { const next = await api<Settings>('/api/admin/settings'); setSettings(next); setRequired(next.adultPasswordRequired) }, [])
  useEffect(() => { window.scrollTo(0, 0); if (site?.admin) { Promise.all([fetchJobs(), fetchSettings()]).catch(error => setError(message(error))) } }, [site?.admin, fetchJobs, fetchSettings])
  const active = jobs.some(job => job.status === 'queued' || job.status === 'running')
  useEffect(() => { if (!site?.admin || !active) return; const timer = window.setInterval(() => fetchJobs().catch(error => setError(message(error))), 2000); return () => clearInterval(timer) }, [site?.admin, active, fetchJobs])
  const login = async () => { if (busy) return; setBusy('login'); setLoginError(''); try { await mutate('/api/admin/session', 'POST', { password }); setPassword(''); await refreshSite() } catch (error) { setLoginError(message(error)) } finally { setBusy('') } }
  const logout = async () => { try { await mutate('/api/admin/session', 'DELETE'); await refreshSite(); invalidate() } catch (error) { setError(message(error)) } }
  const retry = async (id: string) => { setBusy(id); setError(''); try { await mutate(`/api/admin/jobs/${id}/retry`, 'POST'); await fetchJobs() } catch (error) { setError(message(error)) } finally { setBusy('') } }
  const saveSettings = async () => { if (busy) return; setBusy('settings'); setSettingsError(''); setSettingsSaved(false); try { const next = await mutate<Settings>('/api/admin/settings', 'PATCH', { adultPasswordRequired: required, adultPassword }); setSettings(next); setAdultPassword(''); setSettingsSaved(true); await refreshSite(); invalidate() } catch (error) { setSettingsError(message(error)) } finally { setBusy('') } }
  const changePassword = async () => { if (busy) return; setBusy('password'); setPasswordError(''); setPasswordSaved(false); try { await mutate('/api/admin/password', 'POST', { currentPassword, newPassword }); setCurrentPassword(''); setNewPassword(''); setPasswordSaved(true) } catch (error) { setPasswordError(message(error)) } finally { setBusy('') } }

  if (!site) return <main className="page-shell"><div className="loading-line" /></main>
  if (!site.admin) return <main className="login-page"><div className="login-symbol"><ShieldCheck size={28} strokeWidth={1.5} /></div><h1>管理员登录</h1><form onSubmit={event => { event.preventDefault(); login() }}><Field label="管理员密码" value={password} onChange={setPassword} type="password" autoFocus required /><ErrorNotice>{loginError}</ErrorNotice><Button type="submit" fullWidth isDisabled={!!busy}>{busy === 'login' ? '正在登录' : '登录'}</Button></form><Link to="/" className="back-link">返回图册</Link></main>

  return <main className="page-shell admin-page"><div className="page-heading"><h1>管理收藏</h1><Button variant="ghost" onPress={logout}><LogOut size={16} />退出登录</Button></div><div id="add"><ImportPanel onEnqueued={fetchJobs} initialPath={settings?.pixivDirectory} /></div><ErrorNotice>{error}</ErrorNotice>
    <section className="admin-panel jobs-panel"><div className="panel-title"><span className="panel-icon"><Clock3 size={20} /></span><h2>导入任务</h2><Button className="panel-refresh" variant="ghost" isIconOnly aria-label="刷新任务" onPress={() => fetchJobs().catch(error => setError(message(error)))}><RefreshCw size={16} /></Button></div>{!jobs.length ? <div className="panel-empty">暂无任务</div> : <div className="jobs-list">{jobs.map(job => <div className="job-row" key={job.id}><div className={`job-indicator status-${job.status}`}>{job.status === 'completed' ? <Check size={17} /> : job.status === 'running' ? <RefreshCw size={16} className="spin" /> : <Download size={16} />}</div><div className="job-main"><div className="job-name">{jobName(job)}</div><div className="job-meta"><span className={`job-status status-${job.status}`}>{statusLabels[job.status] || job.status}</span>{job.total > 0 && <span>{job.progress.toLocaleString()} / {job.total.toLocaleString()}</span>}<time dateTime={job.createdAt}>{dateLabel(job.createdAt)}</time></div>{job.items && <div className="job-summary">成功 {job.succeeded} · 已存在 {job.existing} · 失败 {job.failed}</div>}{job.phase && job.status === 'running' && <div className="job-summary">{({ parsing: '正在解析', downloading: '正在下载', copying: '正在复制' } as Record<string, string>)[job.phase]}{job.bytes > 0 && ' · ' + (job.bytes / 1024 / 1024).toFixed(1) + ' MB'}</div>}{job.error && <div className="job-error">{job.error}</div>}{!!job.items?.length && <details className="job-details"><summary>作品明细</summary>{job.items.map(item => <div key={item.id} className="job-item"><span>{item.workId ? <Link to={'/works/' + item.workId}>{item.input}</Link> : item.input}</span><span>{statusLabels[item.status] || item.status}</span>{item.error && <div className="job-error">{item.error}</div>}</div>)}</details>}{job.status === 'running' && job.total > 0 && <progress aria-label="任务进度" max={job.total} value={job.progress} />}</div>{job.workId && job.status === 'completed' && <Link to={`/works/${job.workId}`} className="job-open" aria-label="查看作品"><ArrowUpRight size={18} /></Link>}{['failed', 'interrupted', 'partial'].includes(job.status) && <Button size="sm" variant="secondary" onPress={() => retry(job.id)} isDisabled={!!busy}>{job.items?.length ? '重试失败项' : '重试'}</Button>}</div>)}</div>}</section>
    <section className="admin-panel settings-panel"><div className="panel-title"><span className="panel-icon"><ShieldCheck size={20} /></span><h2>R18 浏览设置</h2></div><div className="settings-form"><Switch isSelected={required} onChange={value => { setRequired(value); setSettingsSaved(false) }}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control>开启 R18 需要密码</Switch.Content></Switch><Field label={settings?.adultPasswordSet ? '新密码（选填）' : 'R18 浏览密码'} value={adultPassword} onChange={value => { setAdultPassword(value); setSettingsSaved(false) }} type="password" /><ErrorNotice>{settingsError}</ErrorNotice><div className="settings-submit"><Button onPress={saveSettings} isDisabled={!!busy}>{busy === 'settings' ? '正在保存' : '保存设置'}</Button>{settingsSaved && <span className="success-notice" role="status"><Check size={15} />已保存</span>}</div></div></section>
    <section className="admin-panel settings-panel"><div className="panel-title"><span className="panel-icon"><ShieldCheck size={20} /></span><h2>管理员密码</h2></div><form className="settings-form" onSubmit={event => { event.preventDefault(); changePassword() }}><Field label="当前密码" value={currentPassword} onChange={value => { setCurrentPassword(value); setPasswordSaved(false) }} type="password" required /><Field label="新密码" autoComplete="new-password" value={newPassword} onChange={value => { setNewPassword(value); setPasswordSaved(false) }} type="password" required /><ErrorNotice>{passwordError}</ErrorNotice><div className="settings-submit"><Button type="submit" isDisabled={!!busy}>{busy === 'password' ? '正在修改' : '修改密码'}</Button>{passwordSaved && <span className="success-notice" role="status"><Check size={15} />密码已修改</span>}</div></form></section>
  </main>
}
