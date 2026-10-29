import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { BrowserRouter, Link, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { Button, Input, TextField } from '@heroui/react'
import { Bookmark, Search } from 'lucide-react'
import { api, message, mutate } from './api'
import type { Site } from './api'
import { Dialog, ErrorNotice, Field } from './ui'
import Gallery, { clearGalleryCache } from './Gallery'
import ThemeChoice from './ThemeChoice'
import './App.css'

const WorkPage = lazy(() => import('./WorkPage'))
const Admin = lazy(() => import('./Admin'))
const Reader = lazy(() => import('./Reader'))

interface AppContextValue { site: Site | null; adult: boolean; requestAdult: () => void; disableAdult: () => void; refreshSite: () => Promise<void>; revision: number; invalidate: () => void }
const AppContext = createContext<AppContextValue | null>(null)
export function useApp() { const context = useContext(AppContext); if (!context) throw new Error('页面尚未就绪'); return context }

function Shell({ children }: { children: ReactNode }) {
  const { adult } = useApp()
  const location = useLocation()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  useEffect(() => setSearch(new URLSearchParams(location.search).get('q') || ''), [location.search])
  return <><header className="site-header"><div className="header-inner">
    <Link to="/" className="brand" aria-label="作品收藏首页"><span className="brand-mark"><Bookmark size={22} strokeWidth={2.2} /></span><span>作品收藏</span></Link>
    <form className="header-search" role="search" onSubmit={event => { event.preventDefault(); const params = new URLSearchParams(location.pathname === '/' ? location.search : ''); params.delete('q'); if (search.trim()) params.set('q', search.trim()); if (adult) params.set('adult', '1'); navigate(`/?${params}`) }}><Search size={18} /><TextField aria-label="搜索作品、作者或标签"><Input name="q" placeholder="搜索作品、作者或标签" value={search} onChange={event => setSearch(event.target.value)} /></TextField></form>
    <ThemeChoice />
  </div></header>{children}</>
}

function Application() {
  const location = useLocation()
  const navigate = useNavigate()
  const [site, setSite] = useState<Site | null>(null)
  const [adult, setAdult] = useState(() => sessionStorage.getItem('collectionAdult') === 'true')
  const [revision, setRevision] = useState(0)
  const [unlock, setUnlock] = useState(false)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [siteError, setSiteError] = useState('')
  const [busy, setBusy] = useState(false)
  const refreshSite = useCallback(async () => { const result = await api<Site>('/api/site'); setSite(result); setSiteError('') }, [])
  useEffect(() => { refreshSite().catch(error => setSiteError(message(error))) }, [refreshSite])
  useEffect(() => { sessionStorage.setItem('collectionAdult', String(adult)) }, [adult])
  useEffect(() => { if (site && !site.adultUnlocked && adult) { setAdult(false); clearGalleryCache() } }, [site, adult])
  useEffect(() => {
    if (!adult) return
    const check = () => refreshSite().catch(error => setSiteError(message(error)))
    const timer = window.setInterval(check, 30000)
    window.addEventListener('focus', check)
    return () => { clearInterval(timer); window.removeEventListener('focus', check) }
  }, [adult, refreshSite])
  const invalidate = useCallback(() => { clearGalleryCache(); setRevision(value => value + 1) }, [])
  const setAdultView = (value: boolean) => { setAdult(value); if (location.pathname === '/') { const params = new URLSearchParams(location.search); if (value) params.set('adult', '1'); else params.delete('adult'); navigate(`/?${params}`, { replace: true }) } }
  const requestAdult = async () => { try { const current = await api<Site>('/api/site'); setSite(current); if (current.adultUnlocked) setAdultView(true); else { setPassword(''); setError(''); setUnlock(true) } } catch (error) { setSiteError(message(error)) } }
  const disableAdult = () => { setAdultView(false); clearGalleryCache(); mutate('/api/adult/session', 'DELETE').then(refreshSite).catch(error => setSiteError(message(error))) }
  const submit = async () => { setBusy(true); setError(''); try { await mutate('/api/adult/session', 'POST', { password }); await refreshSite(); setAdultView(true); setUnlock(false); setPassword('') } catch (error) { setError(message(error)) } finally { setBusy(false) } }
  return <AppContext.Provider value={{ site, adult, requestAdult, disableAdult, refreshSite, revision, invalidate }}><Shell>
    {siteError && <div className="page-shell"><ErrorNotice>{siteError}</ErrorNotice><Button variant="secondary" onPress={() => refreshSite().catch(error => setSiteError(message(error)))}>重试连接</Button></div>}
    <Suspense fallback={<main className="page-shell"><div className="loading-line" /></main>}><Routes><Route path="/" element={<Gallery />} /><Route path="/works/:id" element={<WorkPage />} /><Route path="/works/:id/read" element={<Reader />} /><Route path="/admin" element={<Admin />} /><Route path="*" element={<main className="empty-state"><h1>页面不存在</h1><Link to="/">返回图册</Link></main>} /></Routes></Suspense>
  </Shell><Dialog title="R18 浏览密码" open={unlock} onClose={() => setUnlock(false)} footer={<Button onPress={submit} isDisabled={busy}>{busy ? '正在验证' : '开启 R18'}</Button>}>
    <form onSubmit={event => { event.preventDefault(); submit() }}><Field label="密码" value={password} onChange={setPassword} type="password" autoFocus required /><ErrorNotice>{error}</ErrorNotice><button type="submit" className="sr-only" tabIndex={-1}>开启 R18</button></form>
  </Dialog></AppContext.Provider>
}
export default function App() { return <BrowserRouter><Application /></BrowserRouter> }
