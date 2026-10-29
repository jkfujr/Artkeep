import { useEffect, useRef, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { Button } from '@heroui/react'
import { ArrowLeft, BookOpen, ChevronLeft, ChevronRight, List } from 'lucide-react'
import DOMPurify from 'dompurify'
import { makeBook, View } from 'foliate-js/view.js'
import type { Book, TOCItem } from 'foliate-js/view.js'
import { api, ApiError, message } from './api'
import type { Work } from './api'
import { ErrorNotice } from './ui'
import { useApp } from './App'

async function restrictContent(data: unknown, type: string) {
  const source = await data
  if (!['application/xhtml+xml', 'text/html', 'image/svg+xml'].includes(type)) return source
  const text = typeof source === 'string' ? source : source instanceof Blob ? await source.text() : String(source)
  if (type === 'image/svg+xml') return DOMPurify.sanitize(text, { USE_PROFILES: { svg: true, svgFilters: true }, FORBID_TAGS: ['script', 'foreignObject'] })
  const safe = DOMPurify.sanitize(text, { WHOLE_DOCUMENT: true, RETURN_DOM: true, FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'form', 'meta', 'base', 'foreignObject'], FORBID_ATTR: ['srcdoc'], ADD_ATTR: ['epub:type', 'xmlns:epub'], ALLOWED_URI_REGEXP: /^(?:(?:blob|data):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i })
  const doc = new DOMParser().parseFromString(new XMLSerializer().serializeToString(safe), type === 'text/html' ? 'text/html' : 'application/xhtml+xml')
  if (type !== 'image/svg+xml') {
    const head = doc.querySelector('head')
    if (head) { const policy = doc.createElementNS('http://www.w3.org/1999/xhtml', 'meta'); policy.setAttribute('http-equiv', 'Content-Security-Policy'); policy.setAttribute('content', "default-src 'none'; script-src 'none'; style-src 'unsafe-inline' blob:; img-src blob: data:; font-src blob: data:; media-src blob:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'"); head.prepend(policy) }
  }
  return new XMLSerializer().serializeToString(doc)
}

export default function Reader() {
  const { id } = useParams()
  const [params] = useSearchParams()
  const selectedAsset = params.get('asset')
  const { adult, site, requestAdult, refreshSite } = useApp()
  const [work, setWork] = useState<Work | null>(null)
  const [error, setError] = useState('')
  const [locked, setLocked] = useState(false)
  const [loading, setLoading] = useState(true)
  const [retry, setRetry] = useState(0)
  const [toc, setToc] = useState<TOCItem[]>([])
  const [tocOpen, setTocOpen] = useState(() => matchMedia('(min-width: 801px)').matches)
  const [section, setSection] = useState(0)
  const [sectionCount, setSectionCount] = useState(0)
  const [font, setFont] = useState(() => Math.min(32, Math.max(14, Number(localStorage.getItem('artkeepReaderFont')) || 18)))
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<View | null>(null)
  const fontRef = useRef(font)
  const style = useRef<() => void>(() => {})
  useEffect(() => {
    const controller = new AbortController(); setWork(null); setError(''); setLocked(false); setLoading(true)
    api<Work>(`/api/works/${id}?includeAdult=${adult}`, { signal: controller.signal }).then(setWork).catch(error => { if (controller.signal.aborted) return; setError(message(error)); setLocked(error instanceof ApiError && ['adult_locked', 'adult_disabled'].includes(error.code)); setLoading(false) })
    return () => controller.abort()
  }, [id, adult, retry])
  useEffect(() => {
    fontRef.current = font; localStorage.setItem('artkeepReaderFont', String(font)); style.current()
  }, [font])
  useEffect(() => {
    if (!work || work.rating > 0 && (!adult || !site?.adultUnlocked)) return
    const asset = work.assets?.find(item => item.id === selectedAsset && item.format === 'epub') || work.assets?.find(item => item.format === 'epub' && item.available)
    if (!asset) { setError('没有可阅读的书籍'); setLoading(false); return }
    const controller = new AbortController(); let active = true; let book: Book | null = null; let current: View | null = null
    const key = `artkeepReading:${work.id}:${asset.id}`
    const observer = new MutationObserver(() => style.current())
    const open = async () => {
      setLoading(true); setError(''); setToc([])
      const response = await fetch(asset.url, { credentials: 'same-origin', signal: controller.signal })
      if (!response.ok) { if (response.status === 403) { setLocked(true); await refreshSite() } throw new Error(response.status === 403 ? '请输入 R18 浏览密码' : '书籍读取失败') }
      book = await makeBook(new File([await response.blob()], `${work.id}.epub`, { type: 'application/epub+zip' }))
      if (!active) { book.destroy(); return }
      book.transformTarget?.addEventListener('load', event => { const detail = (event as CustomEvent).detail; if (detail.isScript) detail.allow = false })
      book.transformTarget?.addEventListener('data', event => { const detail = (event as CustomEvent).detail; detail.data = restrictContent(detail.data, detail.type) })
      current = new View(); view.current = current; host.current?.replaceChildren(current)
      current.addEventListener('external-link', event => event.preventDefault())
      current.addEventListener('relocate', event => { const detail = (event as CustomEvent).detail; if (active && detail.cfi) localStorage.setItem(key, detail.cfi) })
      current.addEventListener('load', event => { if (active) setSection((event as CustomEvent).detail.index) })
      await current.open(book)
      if (!active) { current.close(); book.destroy(); return }
      current.renderer.setAttribute('flow', 'scrolled'); current.renderer.setAttribute('max-column-count', '1')
      style.current = () => { if (!current?.renderer) return; const root = getComputedStyle(document.documentElement); const fg = root.getPropertyValue('--foreground').trim(); const bg = root.getPropertyValue('--surface').trim(); current.renderer.setStyles(`html,body{color:${fg}!important;background:${bg}!important}body{font-size:${fontRef.current}px!important;line-height:1.9!important}img,svg{max-width:100%;height:auto}a{color:inherit}h1,h2,h3{line-height:1.5}`) }
      style.current(); observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
      setToc(book.toc?.length ? book.toc : book.sections.map((item, i) => ({ label: `第 ${i + 1} 章`, href: item.id }))); setSectionCount(book.sections.length)
      const saved = localStorage.getItem(key) || undefined
      await current.init({ lastLocation: saved, showTextStart: !saved })
      if (!active) return
      if (!current.renderer.getContents().length) await current.goTo(0)
      if (!current.renderer.getContents().length) throw new Error('书籍正文无法显示')
      setLoading(false)
    }
    open().catch(error => { if (active) { setError(message(error)); setLoading(false); current?.close(); book?.destroy(); host.current?.replaceChildren() } })
    return () => { active = false; controller.abort(); observer.disconnect(); style.current = () => {}; current?.close(); book?.destroy(); host.current?.replaceChildren(); view.current = null }
  }, [work, selectedAsset, adult, site?.adultUnlocked, refreshSite])
  const restricted = !!work && work.rating > 0 && (!adult || !site?.adultUnlocked)
  const items = (values: TOCItem[]) => <ul>{values.map((item, index) => <li key={`${item.href}-${index}`}><button type="button" onClick={() => { view.current?.goTo(item.href); if (matchMedia('(max-width: 800px)').matches) setTocOpen(false) }}>{item.label}</button>{item.subitems && items(item.subitems)}</li>)}</ul>
  return <main className="page-shell reader-page"><Link to={`/works/${id}`} className="back-link"><ArrowLeft size={16} />返回作品</Link><div className="reader-toolbar"><h1>{work?.title || '在线阅读'}</h1><Button variant="secondary" onPress={() => setTocOpen(value => !value)} aria-expanded={tocOpen}><List size={16} />目录</Button><div className="reader-font"><Button isIconOnly size="sm" variant="ghost" aria-label="缩小字号" isDisabled={font <= 14} onPress={() => setFont(value => value - 2)}>A−</Button><span>{font}</span><Button isIconOnly size="sm" variant="ghost" aria-label="放大字号" isDisabled={font >= 32} onPress={() => setFont(value => value + 2)}>A＋</Button></div></div>
    <ErrorNotice>{error}</ErrorNotice>{(locked || restricted) && <Button onPress={requestAdult}><BookOpen size={16} />开启 R18</Button>}{error && !locked && <Button variant="secondary" onPress={() => setRetry(value => value + 1)}>重试</Button>}
    {!locked && !restricted && <><div className="reader-layout" style={!tocOpen ? { gridTemplateColumns: 'minmax(0,1fr)' } : undefined}>{tocOpen && <nav className="reader-toc" aria-label="小说目录">{items(toc)}</nav>}<div className="reader-container" ref={host} aria-busy={loading} /></div>{loading && <div className="loading-line" aria-label="正在加载正文" />}<div className="reader-navigation"><Button variant="ghost" aria-label="上一章" isDisabled={loading || section <= 0} onPress={() => view.current?.goTo(section - 1)}><ChevronLeft size={18} />上一章</Button><span>{sectionCount > 0 ? `${section + 1} / ${sectionCount}` : ''}</span><Button variant="ghost" aria-label="下一章" isDisabled={loading || section >= sectionCount - 1} onPress={() => view.current?.goTo(section + 1)}>下一章<ChevronRight size={18} /></Button></div></>}
  </main>
}
