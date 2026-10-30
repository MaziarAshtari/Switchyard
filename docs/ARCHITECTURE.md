# Switchyard architecture

Switchyard is one Go binary that serves three things on one port:

| Path        | What                                                        |
|-------------|-------------------------------------------------------------|
| `/v1/*`     | Client-facing AI API (OpenAI- and Anthropic-compatible)     |
| `/api/*`    | JSON API used by the GUI (session cookie auth)              |
| `/healthz`  | Liveness probe                                              |
| everything else | The React GUI, embedded with `go:embed`                 |

State lives in one SQLite file (`$DATA_DIR/switchyard.db`, pure-Go driver, WAL mode).

## Modules

```
cmd/switchyard          main: config -> db -> services -> HTTP server; subcommands
                        `healthcheck` and `reset-password`
internal/config         environment variables
internal/secret         AES-GCM encryption of upstream keys / proxy passwords,
                        HMAC fingerprints (dedupe), masking
internal/db             open SQLite + run embedded SQL migrations (internal/db/migrations)
internal/store          all SQL: providers, keys, proxies, models, gateway keys,
                        settings, sessions, request logs, video jobs
internal/routing        PURE routing logic: target ordering, key ordering,
                        outcome classification, cooldowns, retry plan.
                        Clock and randomness are injected.
internal/proxy          proxy line parsing (bulk import), round-robin assignment,
                        one cached http.Client per proxy, connectivity test
internal/sse            minimal Server-Sent Events reader/writer
internal/adapter        canonical chat types + Inbound/Upstream interfaces
internal/adapter/openai     OpenAI chat format (inbound + upstream)
internal/adapter/anthropic  Anthropic Messages format (inbound + upstream)
internal/adapter/gemini     Gemini generateContent format (upstream only)
internal/gateway        /v1 handlers: auth, pipeline, attempts, streaming, logging
internal/admin          /api handlers for the GUI, sessions, login rate limit
internal/server         chi router wiring + embedded GUI serving
web/                    React + TypeScript + Vite + Tailwind + shadcn/ui GUI
```

Dependencies: `chi` (router) and `modernc.org/sqlite` (driver). Everything else is the
standard library (SOCKS5 proxies are supported natively by `net/http`).

## Data model

```
providers      id, name, type(openai|anthropic|gemini), base_url, headers(JSON),
               timeout_seconds, enabled, proxy_id?, key_strategy
api_keys       id, provider_id, label, secret_enc, secret_hash, secret_hint, enabled,
               proxy_mode(inherit|direct|specific), proxy_id?,
               status(active|cooling_down|disabled), cooldown_until, last_error,
               last_used_at, success_count, failure_count
proxies        id, name, type(http|https|socks5), host, port, username, password_enc, enabled
models         id, name (public), type(chat|embedding|image|video|tts|asr), strategy,
               max_total_attempts, max_keys_per_target, retry_on, attempt_timeout_seconds
model_targets  id, model_id, position (= priority), provider_id, upstream_model, weight, enabled
gateway_keys   id, name, secret_hash, secret_hint, enabled
request_logs   id, created_at, endpoint, model, model_type, provider, key_hint,
               upstream_model, status_code, latency_ms, stream, input/output tokens,
               attempts, error, attempt_details(JSON), request_body?, response_body?
video_jobs     id (upstream video id), model, provider_id, key_id
sessions       token_hash, expires_at
settings       key/value (password hash, retention, cooldown, body logging, proxy test URL)
```

Times are stored as Unix milliseconds. Secrets are stored encrypted (`secret_enc`,
`password_enc`) and are never returned by the API; `secret_hint` is the masked form.

## Request flow (`/v1/*`)

```
auth (gateway key) -> resolve model -> [pre-processors] -> route/retry -> adapter -> [post-processors] -> log
```

1. **Auth**: `Authorization: Bearer`, `x-api-key` or `x-goog-api-key` must match an enabled
   gateway key (compared by SHA-256 hash).
2. **Resolve**: read the body, take the `model` field (JSON or multipart), load the public
   model and its targets. The model type must match the endpoint.
3. **Pre-processors**: `gateway.PreProcessor` hook (no implementations yet).
4. **Route/retry**: `routing.Plan` yields attempts `(target, key)`:
   - targets are ordered by the model strategy (priority / round_robin / weighted / random);
     whatever comes first, the rest follow as failover;
   - keys of the target's provider are ordered by the provider key strategy
     (round_robin / random / least_recently_used), skipping disabled, cooling-down keys and
     keys whose proxy is disabled;
   - `max_keys_per_target` and `max_total_attempts` bound the loop.
   Each outcome is classified by `routing.Decide`:

   | Outcome                      | Key effect                           | Retry?                  |
   |------------------------------|--------------------------------------|-------------------------|
   | 2xx                          | success counter, status active       | no (done)               |
   | 429                          | cooldown (Retry-After or default)    | if `retry_on` has 429   |
   | 401 / 402 / 403              | disabled + last_error                | yes, next key           |
   | 5xx                          | failure counter                      | if `retry_on` has 5xx   |
   | timeout / connection error   | failure counter                      | if `retry_on` has it    |
   | other 4xx                    | none                                 | no, error to client     |

5. **Adapter**: for each attempt the key's proxy is resolved (specific / provider default /
   direct) and the matching cached `http.Client` is used. Chat requests are translated:
   - same format (OpenAI->openai, Anthropic->anthropic): body forwarded with only `model`
     replaced, response bytes relayed unchanged (usage read on the side);
   - otherwise: inbound format -> canonical `adapter.Request` -> upstream format, and the
     response/stream back the same way.
   Non-chat endpoints are passed through to openai-type providers with `model` replaced.
   Streaming: the first upstream event is read before anything is sent to the client, so a
   failure before that point can still be retried. After the first byte, never.
6. **Post-processors**: `gateway.PostProcessor` hook (no implementations yet).
7. **Log**: one `request_logs` row (with per-attempt details), written by a background writer.
   Old rows are deleted hourly according to the retention setting.

Timeouts: the model's *attempt timeout* bounds an attempt until the response is committed to
the client (whole non-streaming exchange, or until the first stream event). The provider's
*request timeout* caps the entire upstream request, including a long stream.

## Protocol adapters

`internal/adapter` defines a protocol-neutral chat model (system prompt, messages made of
text / image / tool-call / tool-result parts, tools, usage, stop reason, stream events) and:

- `Inbound` (client side): decode request, encode response, encode stream, encode error.
- `Upstream` (provider side): URLs, auth headers, encode request, decode response,
  decode stream, extract error messages.

OpenAI and Anthropic implement both; Gemini implements `Upstream`. Adding a provider type
means adding one package that implements `Upstream`.

## Admin API (`/api/*`)

Session cookie (`HttpOnly`, `SameSite=Strict`) obtained via `POST /api/auth/login`; login
attempts are rate limited per IP. Resources: `providers`, `providers/{id}/keys`, `keys`,
`proxies` (+ `import`, `test`, `assign`), `models` (+ `test`), `logs`, `dashboard`,
`settings`, `gateway-keys`. Errors are `{"error": "...", "fields": {...}}`.

## Frontend

`web/` is a Vite SPA (React, TypeScript, Tailwind, shadcn/ui, TanStack Query,
react-router). `pnpm build` writes `web/dist`, which `web/embed.go` embeds; unknown paths
fall back to `index.html`. Pages: Dashboard, Providers (+ detail), Proxies, Models
(+ editor), Logs, Settings, plus Login.
