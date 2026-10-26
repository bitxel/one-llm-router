# OpenRouter example (live verified)

On 2026-10-24, the router successfully forwarded text and JSON requests to
OpenRouter's Mercury Decide free endpoint, including noul, choice and score
questions. Configure an API-key account with:

- Base URL: `https://openrouter.ai/api/v1`
- Capability: Decision / System One
- Manual model: `inception/mercury-decide:free`
- API key: an OpenRouter key, stored in the account configuration.

```sh
curl "$ROUTER_BASE_URL/v1/systemone" \
  -H "Authorization: Bearer $ROUTER_CLIENT_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"inception/mercury-decide:free","state":"订单重复扣款，请退款。","questions":{"billing":{"type":"noul","instructions":"是否为账单问题？"}}}'
```

The upstream URL is `https://openrouter.ai/api/v1/systemone`; no special
OpenRouter adapter is required. The observed responses reported `usage.cost=0`.
Free availability and rate limits remain subject to OpenRouter's current policy.
This model was present in its model endpoint details but absent from the general
`/models` list when checked, so add it manually.

Official endpoint contract:
https://openrouter.ai/docs/api/api-reference/systemone/submit-a-system-one-request

# OpenCode example

1. Open https://opencode.ai/docs/zen/#jev and follow its Console sign-in link.
2. Create an API key in OpenCode Console. Keep the key out of source control.
3. In the router admin portal, create an API-key account with:
   - Base URL: `https://opencode.ai/inference/systemone/v1`
   - API key: the OpenCode Console key
   - Capability: Decision / System One
4. Optionally add `jev-1.13-free` in the account's manual model list. It is a
   limited-time free model; check the provider before using it. No automatic
   fallback to paid models is implemented.
5. Send a request using the router client key (if client-key auth is enabled):

```sh
curl "$ROUTER_BASE_URL/v1/systemone" \
  -H "Authorization: Bearer $ROUTER_CLIENT_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"jev-1.13-free","state":"订单重复扣款，请退款。","questions":{"billing":{"type":"noul","instructions":"是否为账单问题？"}}}'
```

The account has one Base URL shared by its selected capabilities. Select multiple
capabilities only if that URL serves all corresponding endpoint suffixes.
OpenCode documents different API roots for OpenAI and System One; separate
configured rows can reuse the same provider key. TypeSafe Python SDK text calls
must disable request compression (`compression=False`); SDK model discovery is
not supported through the router's OpenAI-shaped `/v1/models` endpoint.
