import test from 'node:test'
import assert from 'node:assert/strict'
import { api } from '../api.js'

test('上传分块使用二进制并接受 204 空响应', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async (url, init) => {
      assert.equal(url, 'http://127.0.0.1:8080/api/v1/uploads/a/files/0/chunks/0')
      assert.equal(init.credentials, 'omit')
      assert.equal(init.headers.Authorization, 'Bearer test-token')
      assert.equal(init.headers['Content-Type'], 'application/octet-stream')
      return new Response(null, { status: 204 })
    }
    assert.equal(await api({server:'http://127.0.0.1:8080',token:'test-token'}, '/uploads/a/files/0/chunks/0', {method:'PUT',body:new Blob(['abc'])}), null)
  } finally { globalThis.fetch = original }
})
test('Problem Details 转换为操作错误', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => new Response(JSON.stringify({status:401,code:'invalid_token',detail:'访问令牌已失效'}), {status:401})
    await assert.rejects(api({server:'https://artkeep.example',token:'bad'}, '/jobs'), /访问令牌已失效/)
  } finally { globalThis.fetch = original }
})
