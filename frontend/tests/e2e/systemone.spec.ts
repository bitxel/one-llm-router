import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, test } from './fixtures'
import { commitAPIKeySetup } from './helpers'

test('operator adds a multi-capability account and forwards a System One request', async ({
  page,
  request,
}) => {
  const captured: { path?: string; authorization?: string; body?: string } = {}
  const answer = {
    model: 'decision-version',
    answers: { billing: { type: 'noul', noul: 0.98 } },
    usage: { input_tokens: 20, output_tokens: 2 },
  }
  const upstream = createServer(async (req, res) => {
    res.setHeader('content-type', 'application/json')
    if (req.url?.endsWith('/models')) {
      res.end(JSON.stringify({ object: 'list', data: [{ id: 'decision-free', object: 'model' }] }))
      return
    }
    const chunks: Buffer[] = []
    for await (const chunk of req) chunks.push(Buffer.from(chunk))
    captured.path = req.url
    captured.authorization = req.headers.authorization
    captured.body = Buffer.concat(chunks).toString('utf8')
    res.end(JSON.stringify(answer))
  })
  await new Promise<void>((resolve) => upstream.listen(0, '127.0.0.1', resolve))
  const baseURL = `http://127.0.0.1:${(upstream.address() as AddressInfo).port}/gateway/v1`
  try {
    await commitAPIKeySetup(request, { baseURL })
    await page.goto('/admin/accounts/new-apikey')
    await page.getByTestId('apikey-name-input').fill('Multi protocol')
    await page.getByTestId('apikey-api-key-input').fill('test-upstream-key')
    await page.getByTestId('apikey-base-url-input').fill(baseURL)
    const capabilities = ['op.openai.chat_completions', 'op.openai.responses', 'op.systemone']
    for (const capability of capabilities) {
      await page.getByTestId(`capability-${capability}`).check()
    }
    for (const capability of capabilities) {
      await expect(page.getByTestId(`capability-${capability}`)).toBeChecked()
    }
    await page.getByTestId('apikey-submit').click()
    await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/)
    const id = page.url().split('/').pop()
    const accountResponse = await request.get(`/api/admin/accounts/${id}`)
    const account = await accountResponse.json()
    expect(account.code).toBe(0)
    expect(account.data.capabilities).toEqual(capabilities)

    const payload = JSON.stringify({
      model: 'decision-free',
      state: '订单重复扣款，请退款。',
      questions: { billing: { type: 'noul', instructions: '是否为账单问题？' } },
    })
    const response = await request.post('/v1/systemone', {
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer client-placeholder' },
      data: payload,
    })
    expect(response.status()).toBe(200)
    expect(await response.json()).toEqual(answer)
    expect(captured).toEqual({
      path: '/gateway/v1/systemone',
      authorization: 'Bearer test-upstream-key',
      body: payload,
    })
  } finally {
    await new Promise<void>((resolve, reject) =>
      upstream.close((err) => (err ? reject(err) : resolve())),
    )
  }
})
