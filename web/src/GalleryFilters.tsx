import { useEffect, useState } from 'react'
import { Button, Input, Label, Switch, TextField } from '@heroui/react'
import { Check } from 'lucide-react'
import type { AuthorCount, TagCount, YearCount } from './api'
import { Choice, ErrorNotice, Platform } from './ui'

export interface DisplaySettings { columns: string; gap: number; fullWidth: boolean; fit: string }
export const defaultDisplay: DisplaySettings = { columns: 'auto', gap: 24, fullWidth: false, fit: 'contain' }
export function readDisplay(): DisplaySettings {
  try {
    const saved = JSON.parse(localStorage.getItem('collectionDisplay') || '{}')
    return { columns: ['auto', '2', '3', '4', '5', '6'].includes(saved.columns) ? saved.columns : 'auto', gap: [8, 16, 24, 32].includes(saved.gap) ? saved.gap : 24, fullWidth: saved.fullWidth === true, fit: saved.fit === 'crop' ? 'crop' : 'contain' }
  } catch { return defaultDisplay }
}
export const shapeOptions = [{ key: 'landscape', label: '横向' }, { key: 'portrait', label: '竖向' }, { key: 'square', label: '正方形' }, ...['4:3', '16:9', '21:9', '3:4', '9:16', '9:21'].map(key => ({ key, label: key }))]

interface Props {
  params: URLSearchParams; update: (params: URLSearchParams) => void;
  tags: TagCount[]; years: YearCount[]; authors: AuthorCount[];
  tagSearch: string; setTagSearch: (value: string) => void; authorSearch: string; setAuthorSearch: (value: string) => void;
  tagError: string; yearError: string; authorError: string;
  display: DisplaySettings; setDisplay: (value: DisplaySettings) => void;
}

export default function GalleryFilters({ params, update, tags, years, authors, tagSearch, setTagSearch, authorSearch, setAuthorSearch, tagError, yearError, authorError, display, setDisplay }: Props) {
  const [width, setWidth] = useState(params.get('minWidth') || '')
  const [height, setHeight] = useState(params.get('minHeight') || '')
  const minimumWidth = params.get('minWidth') || ''
  const minimumHeight = params.get('minHeight') || ''
  useEffect(() => { setWidth(minimumWidth); setHeight(minimumHeight) }, [minimumWidth, minimumHeight])
  const change = (name: string, value: string) => { const next = new URLSearchParams(params); if (value) next.set(name, value); else next.delete(name); update(next) }
  const toggle = (name: string, value: string) => { const next = new URLSearchParams(params); const values = next.getAll(name); next.delete(name); for (const item of values) if (item !== value) next.append(name, item); if (!values.includes(value)) next.append(name, value); update(next) }
  const platform = params.get('platform') || ''
  return <div className="filter-panel">
    <section className="filter-section"><h2>平台</h2><div className="platform-options">{[{ key: '', label: '全部作品' }, { key: 'pixiv', label: 'Pixiv' }, { key: 'x', label: 'X' }].map(item => <button type="button" key={item.key} aria-pressed={platform === item.key} className={platform === item.key ? 'selected' : ''} onClick={() => { const next = new URLSearchParams(params); if (item.key) next.set('platform', item.key); else next.delete('platform'); next.delete('author'); next.delete('authorName'); update(next) }}><span>{item.label}</span>{platform === item.key && <Check size={15} />}</button>)}</div></section>
    <section className="filter-section"><h2>排序</h2><Choice label="排序" value={params.get('sort') || 'published'} onChange={value => change('sort', value)} options={[{ key: 'published', label: '发布时间' }, { key: 'added', label: '添加时间' }]} /></section>
    <section className="filter-section"><h2>筛选</h2><h3>年份</h3><ErrorNotice>{yearError}</ErrorNotice><div className="filter-chips">{[...years].sort((a, b) => a.year === 'unknown' ? 1 : b.year === 'unknown' ? -1 : b.year.localeCompare(a.year)).map(item => <button type="button" key={item.year} aria-pressed={params.getAll('year').includes(item.year)} onClick={() => toggle('year', item.year)}>{item.year === 'unknown' ? '未知' : item.year}<span>{item.count}</span></button>)}</div>
      <h3>图片比例</h3><div className="filter-chips">{shapeOptions.map(item => <button type="button" key={item.key} aria-pressed={params.getAll('shape').includes(item.key)} onClick={() => toggle('shape', item.key)}>{item.label}</button>)}</div>
      <form className="size-filter" onSubmit={event => { event.preventDefault(); const next = new URLSearchParams(params); for (const [name, value] of [['minWidth', width], ['minHeight', height]]) { if (value && Number(value) > 0) next.set(name, String(Number(value))); else next.delete(name) } update(next) }}><div className="size-fields"><TextField><Label>最小宽度（px）</Label><Input type="number" min={0} max={2147483647} step={1} value={width} onChange={event => setWidth(event.target.value)} placeholder="不限" /></TextField><TextField><Label>最小高度（px）</Label><Input type="number" min={0} max={2147483647} step={1} value={height} onChange={event => setHeight(event.target.value)} placeholder="不限" /></TextField></div><Button type="submit" variant="secondary" size="sm">应用尺寸</Button></form>
      <h3>作者</h3><TextField aria-label="搜索作者"><Input placeholder="搜索作者" value={authorSearch} onChange={event => setAuthorSearch(event.target.value)} /></TextField><ErrorNotice>{authorError}</ErrorNotice><div className="filter-list author-options">{authors.map(author => <button type="button" key={`${author.platform}:${author.id}`} aria-pressed={params.get('author') === author.id && platform === author.platform} onClick={() => { const next = new URLSearchParams(params); next.set('platform', author.platform); next.set('author', author.id); next.set('authorName', author.name); update(next) }}><span className="filter-option-name">{author.name}<Platform platform={author.platform} /></span><span>{author.count}</span></button>)}{!authors.length && !authorError && <span className="filter-empty">没有匹配作者</span>}</div>
      <h3>标签</h3><TextField aria-label="搜索标签"><Input placeholder="搜索标签" value={tagSearch} onChange={event => setTagSearch(event.target.value)} /></TextField><ErrorNotice>{tagError}</ErrorNotice><div className="filter-list tag-options">{tags.map(tag => <button type="button" key={tag.name} aria-pressed={params.getAll('tag').includes(tag.name)} onClick={() => toggle('tag', tag.name)}><span className="filter-option-name">#{tag.name}</span><span>{params.getAll('tag').includes(tag.name) ? <Check size={13} /> : tag.count}</span></button>)}{!tags.length && !tagError && <span className="filter-empty">没有匹配标签</span>}</div>
    </section>
    <section className="filter-section"><h2>显示</h2><div className="display-field"><span>列数</span><Choice label="列数" value={display.columns} onChange={columns => setDisplay({ ...display, columns })} options={[{ key: 'auto', label: '自动' }, ...[2, 3, 4, 5, 6].map(value => ({ key: String(value), label: `${value} 列` }))]} /></div><div className="display-field"><span>间距</span><Choice label="间距" value={String(display.gap)} onChange={gap => setDisplay({ ...display, gap: Number(gap) })} options={[8, 16, 24, 32].map(value => ({ key: String(value), label: `${value} px` }))} /></div><div className="display-field"><span>图片</span><Choice label="图片显示" value={display.fit} onChange={fit => setDisplay({ ...display, fit })} options={[{ key: 'contain', label: '完整显示' }, { key: 'crop', label: '裁切' }]} /></div><Switch className="width-switch" isSelected={display.fullWidth} onChange={fullWidth => setDisplay({ ...display, fullWidth })}><Switch.Content><Switch.Control><Switch.Thumb /></Switch.Control>铺满宽度</Switch.Content></Switch></section>
  </div>
}
