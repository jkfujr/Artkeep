export interface Asset {
  id: string; workId: string; position: number; kind: 'image' | 'animation' | 'video' | 'document';
  format: string; width: number; height: number; duration: number; size: number; available: boolean;
  url: string; thumbnail: string; preview: string;
}
export interface Work {
  id: string; platform: 'pixiv' | 'x' | 'fanbox' | 'local'; sourceKind: string; sourceId: string; sourceUrl: string;
  title: string; description: string; authorId: string; authorName: string; authorUrl: string;
  publishedAt: string; addedAt: string; sourceRating: number; ratingOverride: number | null;
  rating: number; sourceTags: string[]; localTags: string[]; tags: string[]; mediaCount: number;
  available: boolean; cover: Asset | null; assets?: Asset[];
}
export interface WorkList { items: Work[]; total: number; page: number; pageSize: number; hasMore: boolean }
export interface Site { admin: boolean; adultPasswordRequired: boolean; adultUnlocked: boolean }
export interface Settings { adultPasswordRequired: boolean; adultPasswordSet: boolean; pixivDirectory: string; galleryDlAvailable: boolean; ffmpegAvailable: boolean; ffprobeAvailable: boolean; cookiesConfigured: boolean }
export interface JobItem { id: string; input: string; status: string; error: string; workId?: string }
export interface Job { id: string; kind: string; input: string; status: string; progress: number; total: number; error: string; workId?: string; createdAt: string; updatedAt: string; items?: JobItem[]; succeeded: number; existing: number; failed: number; phase?: string; bytes: number; bytesTotal: number }
export interface TagCount { name: string; count: number }
export interface YearCount { year: string; count: number }
export interface AuthorCount { platform: string; id: string; name: string; count: number }

export class ApiError extends Error {
  code: string; status: number;
  constructor(message: string, code: string, status: number) { super(message); this.code = code; this.status = status }
}
export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, { ...options, credentials: 'same-origin', headers: { ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers } });
  const body = await response.json().catch(() => null);
  if (!response.ok) throw new ApiError(body?.error?.message || '请求失败，请重试', body?.error?.code || 'request_failed', response.status);
  return body as T;
}
export function mutate<T>(path: string, method: string, body: unknown = {}): Promise<T> { return api<T>(path, { method, body: JSON.stringify(body) }) }
export function message(error: unknown) { return error instanceof Error ? error.message : '操作失败，请重试' }
export function platformLabel(platform: string) { return ({ pixiv: 'Pixiv', x: 'X', fanbox: 'FANBOX', local: '本地' } as Record<string, string>)[platform] || platform }
export function isVideo(asset: Asset) { return asset.kind === 'video' || ['mp4', 'webm', 'mov'].includes(asset.format) }
export function dateLabel(value: string) { if (!value || Number.isNaN(new Date(value).getTime())) return ''; return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(value)) }
export function fileSize(bytes: number) { return bytes < 1024 * 1024 ? `${Math.round(bytes / 1024)} KB` : `${(bytes / 1024 / 1024).toFixed(1)} MB` }
