import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router-dom'
import { Button, Drawer } from '@heroui/react'
import { MasonryPhotoAlbum } from 'react-photo-album'
import type { Photo } from 'react-photo-album'
import { BookOpen, Images, Layers2, Play, Search, SlidersHorizontal, X } from 'lucide-react'
import { api, ApiError, isVideo, message } from './api'
import type { AuthorCount, TagCount, Work, WorkList, YearCount } from './api'
import { ErrorNotice, Platform, Rating } from './ui'
import { useApp } from './App'
import GalleryFilters, { readDisplay, shapeOptions } from './GalleryFilters'
import 'react-photo-album/masonry.css'

interface Snapshot { items: Work[]; total: number; page: number; hasMore: boolean; scroll: number }
const cache = new Map<string, Snapshot>()
export function clearGalleryCache() { cache.clear() }
const empty: Snapshot = { items: [], total: 0, page: 0, hasMore: false, scroll: 0 }
interface WorkPhoto extends Photo { work: Work; ratio: number }

export default function Gallery() {
  const { adult, revision, site, requestAdult } = useApp()
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const key = `${params}|${adult}|${revision}`
  const latest = useRef(key)
  const [state, setState] = useState<{ key: string; data: Snapshot }>(() => ({ key, data: cache.get(key) || empty }))
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [adultLocked, setAdultLocked] = useState(false)
  const [retry, setRetry] = useState(0)
  const [tags, setTags] = useState<TagCount[]>([])
  const [years, setYears] = useState<YearCount[]>([])
  const [authors, setAuthors] = useState<AuthorCount[]>([])
  const [tagSearch, setTagSearch] = useState('')
  const [authorSearch, setAuthorSearch] = useState('')
  const [tagError, setTagError] = useState('')
  const [yearError, setYearError] = useState('')
  const [authorError, setAuthorError] = useState('')
  const [drawer, setDrawer] = useState(false)
  const [display, setDisplay] = useState(readDisplay)
  const resultsRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  const [compact, setCompact] = useState(() => matchMedia('(max-width: 600px)').matches)
  const data = state.key === key ? state.data : empty
  const selectedTags = params.getAll('tag')
  const query = () => { const q = new URLSearchParams(params); q.delete('adult'); q.set('includeAdult', String(adult)); return q }

  useEffect(() => {
    latest.current = key
    const cached = cache.get(key)
    setError(''); setAdultLocked(false)
    if (cached) { setState({ key, data: cached }); setLoading(false); requestAnimationFrame(() => requestAnimationFrame(() => window.scrollTo(0, cached.scroll))); return }
    setState({ key, data: empty }); setLoading(true); window.scrollTo(0, 0)
    const controller = new AbortController()
    const q = query(); q.set('page', '1')
    api<WorkList>(`/api/works?${q}`, { signal: controller.signal }).then(result => { const next = { ...result, scroll: 0 }; cache.set(key, next); setState({ key, data: next }) }).catch(error => { if (!controller.signal.aborted) { setError(message(error)); setAdultLocked(error instanceof ApiError && error.code === 'adult_locked') } }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [key, retry])
  useEffect(() => { const save = () => { const current = cache.get(key); if (current) current.scroll = window.scrollY }; window.addEventListener('scroll', save, { passive: true }); return () => { save(); window.removeEventListener('scroll', save) } }, [key])
  useEffect(() => {
    const controller = new AbortController()
    setTags([]); setTagError('')
    const timer = window.setTimeout(() => { const q = query(); q.set('prefix', tagSearch); api<TagCount[]>(`/api/tags?${q}`, { signal: controller.signal }).then(setTags).catch(error => { if (!controller.signal.aborted) setTagError(message(error)) }) }, tagSearch ? 180 : 0)
    return () => { clearTimeout(timer); controller.abort() }
  }, [key, tagSearch])
  useEffect(() => {
    const controller = new AbortController()
    setYears([]); setYearError('')
    api<YearCount[]>(`/api/years?${query()}`, { signal: controller.signal }).then(setYears).catch(error => { if (!controller.signal.aborted) setYearError(message(error)) })
    return () => controller.abort()
  }, [key])
  useEffect(() => {
    const controller = new AbortController()
    setAuthors([]); setAuthorError('')
    const timer = window.setTimeout(() => { const q = query(); q.set('prefix', authorSearch); api<AuthorCount[]>(`/api/authors?${q}`, { signal: controller.signal }).then(setAuthors).catch(error => { if (!controller.signal.aborted) setAuthorError(message(error)) }) }, authorSearch ? 180 : 0)
    return () => { clearTimeout(timer); controller.abort() }
  }, [key, authorSearch])
  useEffect(() => { try { localStorage.setItem('collectionDisplay', JSON.stringify(display)) } catch { /* 浏览器未允许保存偏好。 */ } }, [display])
  useEffect(() => {
    const mobile = matchMedia('(max-width: 600px)')
    const desktop = matchMedia('(min-width: 1024px)')
    const update = () => { setCompact(mobile.matches); if (desktop.matches) setDrawer(false) }
    mobile.addEventListener('change', update); desktop.addEventListener('change', update)
    return () => { mobile.removeEventListener('change', update); desktop.removeEventListener('change', update) }
  }, [])
  useLayoutEffect(() => { const element = resultsRef.current; if (!element) return; const observer = new ResizeObserver(entries => setWidth(entries[0].contentRect.width)); observer.observe(element); return () => observer.disconnect() }, [])

  const update = (next: URLSearchParams) => { if (adult) next.set('adult', '1'); else next.delete('adult'); next.delete('page'); setParams(next) }
  const change = (name: string, value: string) => { const next = new URLSearchParams(params); if (value) next.set(name, value); else next.delete(name); update(next) }
  const toggleTag = (tag: string) => { const next = new URLSearchParams(params); const values = next.getAll('tag'); next.delete('tag'); for (const value of values) if (value !== tag) next.append('tag', value); if (!values.includes(tag)) next.append('tag', tag); update(next) }
  const removeMulti = (name: string, value: string) => { const next = new URLSearchParams(params); const remaining = next.getAll(name).filter(item => item !== value); next.delete(name); remaining.forEach(item => next.append(name, item)); update(next) }
  const clear = () => { const next = new URLSearchParams(); if (params.get('sort')) next.set('sort', params.get('sort')!); update(next) }
  const loadMore = async () => { if (loading || !data.hasMore) return; setLoading(true); setError(''); const currentKey = key; const q = query(); q.set('page', String(data.page + 1)); try { const result = await api<WorkList>(`/api/works?${q}`); const next = { ...result, items: [...data.items, ...result.items], scroll: window.scrollY }; cache.set(currentKey, next); if (latest.current === currentKey) setState({ key: currentKey, data: next }) } catch (error) { if (latest.current === currentKey) setError(message(error)) } finally { if (latest.current === currentKey) setLoading(false) } }
  const maxColumns = Math.max(1, Math.floor((width + display.gap) / (140 + display.gap)))
  const autoColumns = compact ? 2 : Math.max(1, Math.floor((width + display.gap) / (240 + display.gap)))
  const columns = Math.min(maxColumns, display.columns === 'auto' ? autoColumns : Number(display.columns))
  const cardWidth = Math.max(1, (width - (columns - 1) * display.gap) / columns)
  const photos = useMemo<WorkPhoto[]>(() => data.items.map(work => { const sourceRatio = work.cover && work.cover.width > 0 && work.cover.height > 0 ? work.cover.height / work.cover.width : 1.25; const ratio = display.fit === 'crop' ? Math.max(0.65, Math.min(1.65, sourceRatio)) : sourceRatio; return { key: work.id, src: work.cover?.thumbnail || '/placeholder.svg', width: cardWidth, height: cardWidth * ratio + (compact ? 72 : 88), work, ratio } }), [data.items, display.fit, cardWidth, compact])
  const pills: { key: string; label: string; remove: () => void }[] = []
  if (params.get('platform')) pills.push({ key: 'platform', label: params.get('platform') === 'pixiv' ? 'Pixiv' : params.get('platform') === 'local' ? '本地' : 'X', remove: () => { const next = new URLSearchParams(params); next.delete('platform'); next.delete('author'); next.delete('authorName'); update(next) } })
  if (params.get('q')) pills.push({ key: 'q', label: params.get('q')!, remove: () => change('q', '') })
  if (params.get('author')) pills.push({ key: 'author', label: params.get('authorName') || '作者', remove: () => { const next = new URLSearchParams(params); next.delete('author'); next.delete('authorName'); update(next) } })
  selectedTags.forEach(tag => pills.push({ key: `tag:${tag}`, label: `#${tag}`, remove: () => toggleTag(tag) }))
  params.getAll('year').forEach(year => pills.push({ key: `year:${year}`, label: year === 'unknown' ? '未知年份' : `${year} 年`, remove: () => removeMulti('year', year) }))
  params.getAll('shape').forEach(shape => pills.push({ key: `shape:${shape}`, label: shapeOptions.find(item => item.key === shape)?.label || shape, remove: () => removeMulti('shape', shape) }))
  for (const [name, label] of [['minWidth', '宽度'], ['minHeight', '高度']]) if (params.get(name)) pills.push({ key: name, label: `${label} ≥ ${params.get(name)} px`, remove: () => change(name, '') })
  const filterPanel = <GalleryFilters onEnableAdult={() => { setDrawer(false); requestAnimationFrame(() => requestAdult()) }} params={params} update={update} tags={tags} years={years} authors={authors} tagSearch={tagSearch} setTagSearch={setTagSearch} authorSearch={authorSearch} setAuthorSearch={setAuthorSearch} tagError={tagError} yearError={yearError} authorError={authorError} display={display} setDisplay={setDisplay} />

  return <main className={`gallery page-shell ${display.fullWidth ? 'gallery-full' : ''}`}>
    <aside className="gallery-sidebar" aria-label="作品筛选">{filterPanel}</aside>
    <div className="gallery-results" ref={resultsRef}>
      <div className="results-heading"><span className="work-count">{loading && !data.items.length ? '正在加载' : <>{data.total.toLocaleString()} <span>部作品</span></>}</span><Drawer isOpen={drawer} onOpenChange={setDrawer}><Button className="mobile-filter-button" variant="secondary" onPress={() => setDrawer(true)}><SlidersHorizontal size={16} />筛选{pills.length > 0 && <span className="filter-count">{pills.length}</span>}</Button><Drawer.Backdrop><Drawer.Content placement="left" className="filter-drawer"><Drawer.Dialog><Drawer.Header><Drawer.Heading>筛选</Drawer.Heading><Drawer.CloseTrigger aria-label="关闭筛选" /></Drawer.Header><Drawer.Body>{filterPanel}</Drawer.Body><Drawer.Footer><Button fullWidth onPress={() => setDrawer(false)}>查看作品</Button></Drawer.Footer></Drawer.Dialog></Drawer.Content></Drawer.Backdrop></Drawer></div>
      {pills.length > 0 && <div className="active-filters">{pills.map(pill => <span className="filter-pill" key={pill.key}>{pill.key === 'q' && <Search size={13} />}{pill.label}<button aria-label={`移除 ${pill.label}`} onClick={pill.remove}><X size={13} /></button></span>)}<button className="clear-filters" onClick={clear}>清除筛选</button></div>}
      {loading && !data.items.length ? <div className="gallery-skeleton" aria-label="正在加载" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0,1fr))`, gap: display.gap }}>{Array.from({ length: columns * 2 }, (_, i) => <div key={i} className="skeleton-card" style={{ height: 260 + i % 3 * 65 }} />)}</div> : <MasonryPhotoAlbum photos={photos} spacing={display.gap} columns={columns} render={{ photo: (_, { photo }) => <article className="work-card" key={photo.work.id}>
        <Link to={`/works/${photo.work.id}`} state={{ from: location.pathname + location.search, assetId: photo.work.cover?.id }} className="work-cover" style={{ aspectRatio: String(1 / photo.ratio) }} aria-label={`查看 ${photo.work.title}`}>
          {photo.work.cover ? <img src={photo.src} alt={photo.work.title} loading="lazy" decoding="async" onError={event => { event.currentTarget.onerror = null; event.currentTarget.src = '/placeholder.svg' }} /> : <div className="document-cover"><BookOpen size={40} strokeWidth={1.2} /><span>EPUB</span></div>}
          <div className="cover-badges"><Rating rating={photo.work.rating} />{photo.work.mediaCount > 1 && <span className="media-count"><Layers2 size={13} />{photo.work.mediaCount}</span>}</div>
          {photo.work.cover && isVideo(photo.work.cover) && <span className="play-badge"><Play size={22} fill="currentColor" /></span>}
        </Link><div className="work-card-meta"><Link to={`/works/${photo.work.id}`} state={{ from: location.pathname + location.search, assetId: photo.work.cover?.id }} className="work-title">{photo.work.title}</Link><div className="work-byline"><span className="author-name">{photo.work.authorName && <Link to={`/?platform=${photo.work.platform}&author=${encodeURIComponent(photo.work.authorId)}&authorName=${encodeURIComponent(photo.work.authorName)}`}>{photo.work.authorName}</Link>}</span><Platform platform={photo.work.platform} /></div><div className="card-tags">{photo.work.tags.slice(0, 3).map(tag => <button key={tag} onClick={() => toggleTag(tag)}>#{tag}</button>)}</div></div>
      </article> }} />}
      <ErrorNotice>{error}</ErrorNotice>{error && <div className="load-more">{adultLocked ? <Button onPress={requestAdult}>开启 R18</Button> : <Button variant="secondary" onPress={() => data.items.length ? loadMore() : setRetry(value => value + 1)}>重试</Button>}</div>}
      {!loading && !error && !data.items.length && <div className="empty-state"><Images size={40} strokeWidth={1.25} /><h2>{pills.length ? '没有找到作品' : '还没有收藏作品'}</h2>{pills.length > 0 ? <Button variant="secondary" onPress={clear}>清除筛选</Button> : site?.admin && <Link className="primary-link" to="/admin">添加作品</Link>}</div>}
      {data.hasMore && !error && <div className="load-more"><Button variant="secondary" onPress={loadMore} isDisabled={loading}>{loading ? '正在加载' : '加载更多'}</Button></div>}
    </div>
  </main>
}
