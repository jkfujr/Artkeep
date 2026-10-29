import { useEffect, useRef, useState } from 'react'
import { Button, Label, TextArea, TextField } from '@heroui/react'
import { FolderOpen, Upload, Download, ArrowLeft } from 'lucide-react'
import { api, fileSize, message, mutate } from './api'
import { Choice, Dialog, ErrorNotice, Field } from './ui'

interface UploadFile { path: string; size: number; modified: number; offset: number }
interface UploadSession { id: string; name: string; status: string; files: UploadFile[]; jobId?: string; chunkSize: number }
interface Directory { path: string; parent: string; directories: { name: string; path: string }[] }
const uploadKey = 'artkeepUpload'

export default function ImportPanel({ onEnqueued, initialPath }: { onEnqueued: () => Promise<void>; initialPath?: string }) {
  const [source, setSource] = useState('url')
  const [urls, setUrls] = useState('')
  const [path, setPath] = useState('')
  const [rating, setRating] = useState('0')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState('')
  const [session, setSession] = useState<UploadSession | null>(null)
  const [progress, setProgress] = useState(0)
  const [directoryOpen, setDirectoryOpen] = useState(false)
  const [directory, setDirectory] = useState<Directory | null>(null)
  const [directoryError, setDirectoryError] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const transfer = useRef<AbortController | null>(null)
  useEffect(() => { if (initialPath) setPath(value => value || initialPath) }, [initialPath])
  useEffect(() => {
    const id = localStorage.getItem(uploadKey)
    if (id) api<UploadSession>(`/api/admin/uploads/${id}`).then(value => { if (value.status === 'uploading') { setSession(value); setProgress(value.files.reduce((n, f) => n + f.offset, 0)) } else localStorage.removeItem(uploadKey) }).catch(() => {})
    return () => transfer.current?.abort()
  }, [])
  const browse = async (next: string) => { setDirectoryError(''); try { setDirectory(await api<Directory>(`/api/admin/directories?path=${encodeURIComponent(next)}`)); setDirectoryOpen(true) } catch (error) { setDirectoryError(message(error)); setDirectoryOpen(true) } }
  const enqueue = async (kind: string) => {
    if (busy) return
    setBusy(true); setError(''); setResult('')
    try { await mutate('/api/admin/imports', 'POST', { kind, rating: Number(rating), ...(kind === 'url' ? { urls: urls.split('\n').map(value => value.trim()).filter(Boolean) } : kind === 'server' ? { path } : {}) }); setResult('已加入任务'); if (kind === 'url') setUrls(''); await onEnqueued() } catch (error) { setError(message(error)) } finally { setBusy(false) }
  }
  const upload = async (selected: FileList | null) => {
    if (!selected?.length || busy) return
    const files = Array.from(selected).sort((a, b) => a.webkitRelativePath.localeCompare(b.webkitRelativePath))
    const controller = new AbortController(); transfer.current = controller; setBusy(true); setError(''); setResult('')
    try {
      const name = files[0].webkitRelativePath.split('/')[0] || '文件夹'
      const savedId = localStorage.getItem(uploadKey)
      let resumeId = savedId
      if (savedId) { const saved = await api<UploadSession>(`/api/admin/uploads/${savedId}`); if (saved.status !== 'uploading') { localStorage.removeItem(uploadKey); setSession(null); setResult('已加入任务'); await onEnqueued(); return } if (saved.name !== name) resumeId = null }
      const next = await mutate<UploadSession>('/api/admin/uploads', 'POST', { id: resumeId || '', name, files: files.map(file => ({ path: file.webkitRelativePath || file.name, size: file.size, modified: file.lastModified, offset: 0 })) })
      localStorage.setItem(uploadKey, next.id); setSession(next)
      let done = next.files.reduce((sum, f) => sum + f.offset, 0); setProgress(done)
      const byPath = new Map(files.map(file => [file.webkitRelativePath || file.name, file])); let index = 0
      const worker = async () => {
        while (index < next.files.length) {
          const i = index++; const saved = next.files[i]; const file = byPath.get(saved.path)!
          let offset = saved.offset
          while (offset < file.size) {
            const chunk = file.slice(offset, offset + next.chunkSize)
            const response = await api<{ offset: number }>(`/api/admin/uploads/${next.id}/files/${i}?offset=${offset}`, { method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: chunk, signal: controller.signal })
            done += response.offset - offset; offset = response.offset; setProgress(done)
          }
        }
      }
      try { await Promise.all([worker(), worker()]) } catch (error) { controller.abort(); throw error }
      if (controller.signal.aborted) return
      await mutate(`/api/admin/uploads/${next.id}/complete`, 'POST', { rating: Number(rating) })
      localStorage.removeItem(uploadKey); setSession(null); setResult('上传完成，已加入任务'); await onEnqueued()
    } catch (error) { if (!controller.signal.aborted || error instanceof Error && error.name !== 'AbortError') setError(message(error)) } finally { setBusy(false); transfer.current = null; if (input.current) input.current.value = '' }
  }
  const cancelUpload = async () => {
    const id = session?.id || localStorage.getItem(uploadKey); if (!id) return
    transfer.current?.abort(); setError('')
    try { await mutate(`/api/admin/uploads/${id}`, 'DELETE'); localStorage.removeItem(uploadKey); setSession(null); setProgress(0); setResult('已取消上传') } catch (error) { setError(message(error)) }
  }
  const total = session?.files.reduce((sum, f) => sum + f.size, 0) || 0
  return <section className="admin-panel import-panel"><div className="panel-title"><span className="panel-icon"><FolderOpen size={20} /></span><h2>导入</h2></div>
    <div className="import-sources" role="group" aria-label="导入来源">{[{ key: 'url', label: 'URL' }, { key: 'local', label: '本机文件夹' }, { key: 'server', label: '服务器文件夹' }].map(item => <button key={item.key} type="button" aria-pressed={source === item.key} disabled={busy} onClick={() => { setSource(item.key); setResult(''); setError('') }}>{item.label}</button>)}</div>
    <form onSubmit={event => { event.preventDefault(); if (source !== 'local') enqueue(source) }}>
      {source === 'url' && <TextField className="field"><Label>作品链接（每行一个）</Label><TextArea rows={5} value={urls} onChange={event => setUrls(event.target.value)} placeholder={'https://www.pixiv.net/artworks/…\nhttps://www.pixiv.net/novel/show.php?id=…\nhttps://x.com/…/status/…'} required /></TextField>}
      {source === 'server' && <div className="server-directory-field"><Field label="服务器路径" value={path} onChange={setPath} required /><Button variant="secondary" onPress={() => browse(path)} isDisabled={busy}><FolderOpen size={16} />浏览</Button></div>}
      <div className="field import-rating"><label>未分级作品</label><Choice label="未分级作品" value={rating} onChange={setRating} options={[{ key: '0', label: '全年龄' }, { key: '1', label: 'R-18' }, { key: '2', label: 'R-18G' }]} /></div>
      {source === 'local' ? <><input ref={input} type="file" multiple {...{ webkitdirectory: '', directory: '' }} className="sr-only" aria-label="选择本机文件夹" onChange={event => upload(event.target.files)} /><div className="import-actions"><Button onPress={() => input.current?.click()} isDisabled={busy}><Upload size={16} />{session ? '重新选择文件夹继续上传' : '选择文件夹上传'}</Button>{session && <Button variant="ghost" onPress={cancelUpload}>取消上传</Button>}</div>{session && <div className="upload-progress" role="status"><span>{session.name} · {fileSize(progress)} / {fileSize(total)}</span><progress aria-label="上传进度" max={total || 1} value={progress} /></div>}</> : <Button type="submit" isDisabled={busy || (source === 'url' ? !urls.trim() : !path.trim())}><Download size={16} />{busy ? '正在添加' : '导入'}</Button>}
    </form><ErrorNotice>{error}</ErrorNotice>{result && <div className="success-notice" role="status">{result}</div>}
    <div className="migration-action"><Button variant="ghost" size="sm" onPress={() => enqueue('migration')} isDisabled={busy}>迁移已有收藏</Button></div>
    <Dialog title="服务器目录" open={directoryOpen} onClose={() => setDirectoryOpen(false)} footer={<Button isDisabled={!directory?.path} onPress={() => { setPath(directory!.path); setDirectoryOpen(false) }}>选择此目录</Button>}>
      <div className="directory-current">{directory?.path || '磁盘'}</div><ErrorNotice>{directoryError}</ErrorNotice><div className="directory-list">{directory?.path && directory.parent !== directory.path && <button type="button" onClick={() => browse(directory.parent)}><ArrowLeft size={16} />上一级</button>}{directory?.directories.map(item => <button type="button" key={item.path} onClick={() => browse(item.path)}><FolderOpen size={16} />{item.name}</button>)}</div>
    </Dialog>
  </section>
}
