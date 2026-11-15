function crc32(bytes) {
  let crc = 0xffffffff
  for (const b of bytes) { crc ^= b; for (let i = 0; i < 8; i++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1)) }
  return (crc ^ 0xffffffff) >>> 0
}
// 在原始 Pixiv ZIP 中追加 animation.json，保持图片帧的原始压缩内容。
export async function ugoira(blob, info) {
  const bytes = new Uint8Array(await blob.arrayBuffer()), view = new DataView(bytes.buffer)
  let end = -1
  for (let i = bytes.length - 22; i >= Math.max(0, bytes.length - 65557); i--) if (view.getUint32(i, true) === 0x06054b50) { end = i; break }
  if (end < 0) throw new Error('Pixiv 动图 ZIP 无效')
  const count = view.getUint16(end + 10, true), size = view.getUint32(end + 12, true), offset = view.getUint32(end + 16, true)
  if (count === 65535 || offset + size !== end || view.getUint16(end + 4, true) !== 0) throw new Error('动图 ZIP 格式不支持')
  const name = new TextEncoder().encode('animation.json'), data = new TextEncoder().encode(JSON.stringify(info)), crc = crc32(data)
  const local = new Uint8Array(30 + name.length + data.length), lv = new DataView(local.buffer)
  lv.setUint32(0, 0x04034b50, true); lv.setUint16(4, 20, true); lv.setUint32(14, crc, true); lv.setUint32(18, data.length, true); lv.setUint32(22, data.length, true); lv.setUint16(26, name.length, true); local.set(name, 30); local.set(data, 30 + name.length)
  const central = new Uint8Array(46 + name.length), cv = new DataView(central.buffer)
  cv.setUint32(0, 0x02014b50, true); cv.setUint16(4, 20, true); cv.setUint16(6, 20, true); cv.setUint32(16, crc, true); cv.setUint32(20, data.length, true); cv.setUint32(24, data.length, true); cv.setUint16(28, name.length, true); cv.setUint32(42, offset, true); central.set(name, 46)
  const ending = bytes.slice(end), ev = new DataView(ending.buffer)
  ev.setUint16(8, count + 1, true); ev.setUint16(10, count + 1, true); ev.setUint32(12, size + central.length, true); ev.setUint32(16, offset + local.length, true)
  return new Blob([bytes.slice(0, offset), local, bytes.slice(offset, end), central, ending], { type: 'application/octet-stream' })
}
