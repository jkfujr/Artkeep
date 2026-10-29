import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { Button, Label, TextArea, TextField } from '@heroui/react'
import { ArrowLeft, ArrowUpRight, BookOpen, CalendarDays, ChevronLeft, ChevronRight, Download, Expand, Pencil, UserRound } from 'lucide-react'
import Lightbox from 'yet-another-react-lightbox'
import type { Slide } from 'yet-another-react-lightbox'
import Zoom from 'yet-another-react-lightbox/plugins/zoom'
import Thumbnails from 'yet-another-react-lightbox/plugins/thumbnails'
import Fullscreen from 'yet-another-react-lightbox/plugins/fullscreen'
import Video from 'yet-another-react-lightbox/plugins/video'
import Counter from 'yet-another-react-lightbox/plugins/counter'
import { api, ApiError, dateLabel, fileSize, isVideo, message, mutate } from './api'
import type { Work } from './api'
import { Choice, Dialog, ErrorNotice, Platform, Rating } from './ui'
import { useApp } from './App'
import 'yet-another-react-lightbox/styles.css'
import 'yet-another-react-lightbox/plugins/thumbnails.css'
import 'yet-another-react-lightbox/plugins/counter.css'

export default function WorkPage() {
  const { id } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const { adult, site, requestAdult, invalidate } = useApp()
  const [work, setWork] = useState<Work | null>(null)
  const [index, setIndex] = useState(0)
  const [lightbox, setLightbox] = useState(false)
  const [error, setError] = useState('')
  const [locked, setLocked] = useState(false)
  const [edit, setEdit] = useState(false)
  const [localTags, setLocalTags] = useState('')
  const [rating, setRating] = useState('source')
  const [saveError, setSaveError] = useState('')
  const [saving, setSaving] = useState(false)
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    const controller = new AbortController(); setWork(null); setError(''); setLocked(false); setIndex(0); setLightbox(false); window.scrollTo(0, 0)
    api<Work>(`/api/works/${id}?includeAdult=${adult}`, { signal: controller.signal }).then(result => { setWork(result); setIndex(Math.max(0, result.assets?.findIndex(item => item.id === location.state?.assetId) ?? 0)) }).catch(error => { if (controller.signal.aborted) return; setError(message(error)); setLocked(error instanceof ApiError && (error.code === 'adult_locked' || error.code === 'adult_disabled')) })
    return () => controller.abort()
  }, [id, adult, retry])
  useEffect(() => { const keydown = (event: KeyboardEvent) => { if (lightbox || edit || ['INPUT', 'TEXTAREA', 'SELECT'].includes((event.target as HTMLElement)?.tagName)) return; const count = work?.assets?.length || 0; if (event.key === 'ArrowRight') { event.preventDefault(); setIndex(value => Math.min(count - 1, value + 1)) } if (event.key === 'ArrowLeft') { event.preventDefault(); setIndex(value => Math.max(0, value - 1)) } }; window.addEventListener('keydown', keydown); return () => window.removeEventListener('keydown', keydown) }, [work, lightbox, edit])
  const assets = work?.assets || []
  const viewable = useMemo(() => assets.filter(asset => asset.available && asset.kind !== 'document'), [assets])
  const asset = assets[index]
  const slides = useMemo<Slide[]>(() => viewable.map(asset => isVideo(asset) ? { type: 'video', width: asset.width, height: asset.height, poster: asset.thumbnail, sources: [{ src: asset.url, type: `video/${asset.format === 'mov' ? 'mp4' : asset.format}` }], loop: asset.kind === 'animation', preload: 'metadata', autoPlay: false } : { type: 'image', src: asset.kind === 'animation' ? asset.url : asset.preview, width: asset.width, height: asset.height, thumbnail: asset.thumbnail, ...(asset.kind === 'animation' ? {} : { srcSet: [{ src: asset.preview, width: Math.round(asset.width * Math.min(1, 1920 / Math.max(asset.width, asset.height))), height: Math.round(asset.height * Math.min(1, 1920 / Math.max(asset.width, asset.height))) }, { src: asset.url, width: asset.width, height: asset.height }] }) }), [viewable])
  const back = typeof location.state?.from === 'string' && location.state.from.startsWith('/') ? location.state.from : '/'
  const openViewer = () => { if (asset?.available && asset.kind !== 'document') setLightbox(true) }
  const beginEdit = () => { if (!work) return; setLocalTags(work.localTags.join('\n')); setRating(work.ratingOverride === null ? 'source' : String(work.ratingOverride)); setSaveError(''); setEdit(true) }
  const save = async () => { if (!work || saving) return; setSaving(true); setSaveError(''); try { const updated = await mutate<Work>(`/api/admin/works/${work.id}`, 'PATCH', { localTags: [...new Set(localTags.split('\n').map(value => value.trim()).filter(Boolean))], ratingOverride: rating === 'source' ? null : Number(rating) }); setWork(updated); invalidate(); setEdit(false) } catch (error) { setSaveError(message(error)) } finally { setSaving(false) } }

  if (error) return <main className="page-shell detail-error"><Link className="back-link" to={back}><ArrowLeft size={17} />返回图册</Link><div className="empty-state"><ErrorNotice>{error}</ErrorNotice>{locked ? <Button onPress={requestAdult}>开启 R18</Button> : <Button variant="secondary" onPress={() => setRetry(value => value + 1)}>重试</Button>}</div></main>
  if (!work) return <main className="page-shell detail-skeleton"><div className="skeleton-card" /><div className="skeleton-card" /></main>
  if (work.rating > 0 && !adult) return <main className="page-shell"><Link className="back-link" to={back}><ArrowLeft size={17} />返回图册</Link><div className="empty-state"><span>请开启 R18 显示</span><Button onPress={requestAdult}>开启 R18</Button></div></main>

  return <main className="page-shell detail-page"><Link className="back-link" to={back}><ArrowLeft size={17} />返回图册</Link><div className="detail-layout"><section className="artwork-viewer" aria-label="作品媒体">
    <div className={`artwork-stage ${asset?.kind === 'document' ? 'document-stage' : ''}`}>
      {!asset?.available ? <div className="empty-state"><BookOpen size={38} /><span>原文件暂不可用</span></div> : asset.kind === 'document' ? <div className="document-cover"><BookOpen size={68} strokeWidth={1} /><span>EPUB</span><Link to={`/works/${work.id}/read?asset=${asset.id}`} className="primary-link"><BookOpen size={16} />在线阅读</Link><a href={`${asset.url}?download=1`} className="secondary-link"><Download size={16} />下载 EPUB</a></div> : isVideo(asset) ? <video key={asset.id} src={asset.url} poster={asset.thumbnail} controls preload="metadata" playsInline loop={asset.kind === 'animation'} /> : <button className="artwork-image-button" onClick={openViewer} aria-label="全屏查看作品"><img src={asset.kind === 'animation' ? asset.url : asset.preview} alt={`${work.title} · ${index + 1}`} onError={event => { if (event.currentTarget.src !== new URL(asset.url, window.location.origin).href) event.currentTarget.src = asset.url }} /></button>}
    </div><div className="viewer-toolbar"><div className="page-controls"><Button isIconOnly variant="ghost" aria-label="上一页" isDisabled={index === 0} onPress={() => setIndex(value => value - 1)}><ChevronLeft size={20} /></Button><span>{index + 1}<span> / {assets.length}</span></span><Button isIconOnly variant="ghost" aria-label="下一页" isDisabled={index >= assets.length - 1} onPress={() => setIndex(value => value + 1)}><ChevronRight size={20} /></Button></div><div className="viewer-tools">{asset && <span className="asset-dimensions">{asset.kind === 'document' ? 'EPUB' : `${asset.width} × ${asset.height}`}<span>{fileSize(asset.size)}</span></span>}{asset?.kind !== 'document' && <Button isIconOnly variant="ghost" aria-label="全屏查看" onPress={openViewer}><Expand size={17} /></Button>}</div></div>
    {assets.length > 1 && <div className="page-thumbnails" aria-label="选择页码">{assets.map((item, position) => <button key={item.id} className={position === index ? 'selected' : ''} onClick={() => setIndex(position)} aria-label={`第 ${position + 1} 页`} aria-current={position === index ? 'true' : undefined}>{item.kind !== 'document' && item.available ? <img src={item.thumbnail} alt="" loading="lazy" /> : <BookOpen size={18} />}<span>{position + 1}</span></button>)}</div>}
  </section><aside className="work-info"><div className="detail-labels"><Platform platform={work.platform} /><Rating rating={work.rating} /></div><h1>{work.title}</h1>{work.authorName && <Link className="detail-author" to={`/?platform=${work.platform}&author=${encodeURIComponent(work.authorId)}&authorName=${encodeURIComponent(work.authorName)}`}><span className="author-avatar"><UserRound size={18} /></span>{work.authorName}</Link>}{work.publishedAt && <div className="detail-date"><CalendarDays size={14} /><time dateTime={work.publishedAt}>{dateLabel(work.publishedAt)}</time></div>}
    <div className="detail-actions">{work.sourceUrl && <a href={work.sourceUrl} target="_blank" rel="noreferrer" className="secondary-link">原作<ArrowUpRight size={16} /></a>}{asset?.available && <a href={`${asset.url}?download=1`} className="primary-link"><Download size={16} />{asset.kind === 'image' ? '原图' : '原文件'}</a>}{site?.admin && <Button isIconOnly variant="tertiary" aria-label="编辑标签和分级" onPress={beginEdit}><Pencil size={16} /></Button>}</div>
    {work.tags.length > 0 && <div className="detail-tags">{work.tags.map(tag => <Link key={tag} to={`/?tag=${encodeURIComponent(tag)}`}>#{tag}</Link>)}</div>}{work.description && <p className="work-description">{work.description}</p>}
  </aside></div>
  {adult || work.rating === 0 ? <Lightbox className="archive-lightbox" open={lightbox} close={() => setLightbox(false)} slides={slides} index={Math.max(0, viewable.findIndex(item => item.id === asset?.id))} on={{ view: ({ index: next }) => { const nextAsset = viewable[next]; if (nextAsset) setIndex(assets.findIndex(item => item.id === nextAsset.id)) } }} plugins={[Zoom, Thumbnails, Fullscreen, Video, Counter]} carousel={{ finite: true, preload: 1 }} video={{ autoPlay: false, preload: 'metadata' }} labels={{ 'Next': '下一页', 'Previous': '上一页', 'Close': '关闭', 'Zoom in': '放大', 'Zoom out': '缩小', 'Enter Fullscreen': '全屏', 'Exit Fullscreen': '退出全屏', 'Show thumbnails': '显示缩略图', 'Hide thumbnails': '隐藏缩略图' }} /> : null}
  <Dialog title="编辑作品" open={edit} onClose={() => setEdit(false)} footer={<><Button variant="ghost" onPress={() => setEdit(false)}>取消</Button><Button onPress={save} isDisabled={saving}>{saving ? '正在保存' : '保存'}</Button></>}>
    <TextField className="field"><Label>本地标签（每行一个）</Label><TextArea rows={5} value={localTags} onChange={event => setLocalTags(event.target.value)} /></TextField><div className="field"><label>展示分级</label><Choice label="展示分级" value={rating} onChange={setRating} options={[{ key: 'source', label: '沿用来源分级' }, { key: '0', label: '全年龄' }, { key: '1', label: 'R-18' }, { key: '2', label: 'R-18G' }]} /></div><ErrorNotice>{saveError}</ErrorNotice>
  </Dialog>
  <button className="sr-only" onClick={() => navigate(back)}>返回图册</button>
  </main>
}
