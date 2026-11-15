import test from 'node:test'
import assert from 'node:assert/strict'
import { extractTweet, pathFor, mediaURL } from '../shared.js'
import { ugoira } from '../zip.js'

test('命名保留 ID 与页序并处理 Windows 文件名', () => {
  assert.equal(pathFor('pixiv/{user_id}-{user}/{pid}-{title}/{id}', {userId:'29959947',user:'Lilith-Abdl',idNum:'151961159',title:'Miyabi (Pool Idea)',id:'151961159_p4'}), 'pixiv/29959947-Lilith-Abdl/151961159-Miyabi (Pool Idea)/151961159_p4')
  assert.equal(pathFor('x/{user_id}/{id}', {userId:'42',id:'CON'}), 'x/42/_CON')
  assert.throws(() => mediaURL('https://evil.example/file.jpg', 'pixiv'))
  assert.throws(() => pathFor('{unknown}', {}))
})

test('X 长文、原图、最高码率视频与敏感标记', () => {
  const raw = {rest_id:'9999999999999999999',core:{user_results:{result:{rest_id:'8888888888888888888',legacy:{name:'作者',screen_name:'artist'}}}},note_tweet:{note_tweet_results:{result:{text:'完整长文'}}},legacy:{full_text:'截断文本',created_at:'Wed Nov 11 17:08:00 +0000 2026',possibly_sensitive:false,entities:{hashtags:[{text:'art'}]},extended_entities:{media:[{type:'photo',media_url_https:'https://pbs.twimg.com/media/example.jpg',id_str:'1'},{type:'animated_gif',id_str:'2',sensitive_media_warning:{adult_content:true},video_info:{variants:[{content_type:'application/x-mpegURL',url:'https://video.twimg.com/a.m3u8'},{content_type:'video/mp4',bitrate:10,url:'https://video.twimg.com/low.mp4'},{content_type:'video/mp4',bitrate:100,url:'https://video.twimg.com/high.mp4'}]}}]}}}
  const result = extractTweet(raw)
  assert.equal(result.sourceId, '9999999999999999999')
  assert.equal(result.meta.author.id, '8888888888888888888')
  assert.equal(result.meta.content, '完整长文')
  assert.equal(result.files[1].url, 'https://video.twimg.com/high.mp4')
  assert.match(result.files[0].url, /name=orig/)
  assert.equal(result.meta.sensitive, true)
  delete raw.legacy.extended_entities.media[1].sensitive_media_warning
  assert.equal(extractTweet(raw).meta.sensitive, false)
})

test('ugoira 追加 animation.json 后 ZIP 目录与原始帧保持有效', async () => {
  const ending = new Uint8Array(22), ev = new DataView(ending.buffer)
  ev.setUint32(0, 0x06054b50, true)
  const blob = await ugoira(new Blob([ending]), {frames:[{file:'frame.png',delay:100}]})
  const bytes = new Uint8Array(await blob.arrayBuffer()), view = new DataView(bytes.buffer)
  assert.equal(view.getUint32(0,true), 0x04034b50)
  const end = bytes.length - 22
  assert.equal(view.getUint16(end+10,true),1)
  const offset = view.getUint32(end+16,true)
  assert.equal(view.getUint32(offset,true),0x02014b50)
  assert.match(new TextDecoder().decode(bytes), /animation.json/)
  await assert.rejects(ugoira(new Blob(['bad']), {}))
})
