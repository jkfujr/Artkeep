export const defaults = {
  server: '', token: '', destination: 'artkeep', takeover: false,
  naming: 'pixiv/{user_id}-{user}/{pid}-{title}/{id}', xNaming: 'x/{user_id}-{user}/{pid}/{id}',
  bookmarkAfterSave: false, bookmarkRestrict: false, downloadThread: 3, downloadInterval: 1,
}
export async function settings() {
  if (!chrome.storage) {
    const result = await chrome.runtime.sendMessage({ type: 'engineSettings' })
    if (!result?.ok) throw new Error(result?.error || '无法读取收藏设置')
    return result.value
  }
  const value = { ...defaults, ...(await chrome.storage.local.get('settings')).settings }
  delete value.ppdExtensionId
  return value
}
export function safeName(value) {
  const text = String(value ?? '').replace(/[<>:"/\\|?*\u0000-\u001f]/g, '_').replace(/[. ]+$/g, '').trim()
  return !text || /^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)/i.test(text) ? `_${text || 'untitled'}` : text
}
export function pathFor(rule, data) {
  const values = { user_id: data.userId, user: data.user, pid: data.idNum, title: data.title, id: data.id }
  if (typeof rule !== 'string' || rule.length > 2000 || /\{(?!user_id\}|user\}|pid\}|title\}|id\})/.test(rule)) throw new Error('命名规则包含未支持字段')
  return rule.split('/').map(part => safeName(part.replace(/\{(user_id|user|pid|title|id)\}/g, (_, key) => safeName(values[key])))).join('/')
}
export function mediaURL(value, platform) {
  const url = new URL(value)
  const hosts = platform === 'pixiv' ? /(^|\.)pximg\.net$/ : /^(pbs|video)\.twimg\.com$/
  if (url.protocol !== 'https:' || url.username || url.password || !hosts.test(url.hostname)) throw new Error('媒体地址无效')
  return url.href
}
export function plainText(html) {
  const doc = new DOMParser().parseFromString(String(html || '').replace(/<br\s*\/?>/gi, '\n'), 'text/html')
  for (const a of doc.querySelectorAll('a')) {
    const raw = a.getAttribute('href') || ''
    if (raw.startsWith('/jump.php?')) { try { a.textContent = decodeURIComponent(raw.slice(10)) } catch { a.textContent = raw } }
    else if (/^https?:\/\//.test(raw)) a.textContent = raw
  }
  return doc.body.textContent.trim()
}
export function sidecars(meta, root) {
  const description = plainText(meta.description), url = `https://www.pixiv.net/artworks/${meta.idNum}`
  const fields = { ID: meta.idNum, URL: url, Original: meta.original, Thumbnail: meta.thumb || '', xRestrict: ['AllAges', 'R-18', 'R-18G'][meta.xRestrict], AI: meta.aiType === 2 ? 'Yes' : 'No', User: meta.user, UserID: meta.userId, Title: meta.title, Description: description, Tags: meta.tags.map(tag => '#' + tag).join('\n'), Size: `${meta.fullWidth} x ${meta.fullHeight}`, Bookmark: meta.bmk || 0, Date: meta.date }
  return [
    { path: root + '-meta.json', content: JSON.stringify(meta, null, 2), mime: 'application/json' },
    { path: root + '-meta.txt', content: Object.entries(fields).map(([k, v]) => `${k}\n${v}\n\n`).join(''), mime: 'text/plain' },
    { path: root + '-简介-links.txt', content: description, mime: 'text/plain' },
  ]
}
export function sourceRating(tweet) {
  const l = tweet.legacy || tweet
  return !!(l.possibly_sensitive || l.extended_entities?.media?.some(media => media.sensitive_media_warning && Object.values(media.sensitive_media_warning).some(Boolean)))
}
export function extractTweet(result) {
  const tweet = result.tweet || result, l = tweet.legacy
  if (!l || !(tweet.rest_id || l.id_str)) return null
  if (l.retweeted_status_result) return extractTweet(l.retweeted_status_result.result)
  const author = tweet.core?.user_results?.result, user = author?.legacy || author?.core
  if (!author?.rest_id || !user) return null
  const id = String(tweet.rest_id || l.id_str), content = tweet.note_tweet?.note_tweet_results?.result?.text || l.full_text || l.text || ''
  const files = []
  for (const media of l.extended_entities?.media || []) {
    if (media.type === 'photo') {
      const url = new URL(media.media_url_https); const extension = url.pathname.split('.').pop()
      url.pathname = url.pathname.replace(/\.[^.]+$/, ''); url.search = new URLSearchParams({ format: extension, name: 'orig' }).toString()
      files.push({ url: mediaURL(url.href, 'x'), ext: extension, type: 'image', mediaId: String(media.id_str || '') })
    } else if (['video', 'animated_gif'].includes(media.type)) {
      const variants = (media.video_info?.variants || []).filter(v => v.content_type === 'video/mp4').sort((a, b) => (b.bitrate || 0) - (a.bitrate || 0))
      if (!variants.length) throw new Error('此帖子没有可保存的 MP4，请重新加载帖子')
      files.push({ url: mediaURL(variants[0].url, 'x'), ext: 'mp4', type: media.type, mediaId: String(media.id_str || '') })
    }
  }
  if (!files.length) return null
  return { platform: 'x', sourceId: id, meta: { category: 'twitter', tweet_id: id, author: { id: String(author.rest_id), nick: user.name || author.core?.name || '', name: user.screen_name || author.core?.screen_name || '' }, content, date: new Date(l.created_at).toISOString(), hashtags: (l.entities?.hashtags || []).map(tag => tag.text), sensitive: sourceRating(tweet), media: files.map(({ type, mediaId }) => ({ type, mediaId })), raw: tweet }, files }
}
