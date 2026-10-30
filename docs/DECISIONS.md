# Decisions

Where the spec was ambiguous, the simplest reasonable option was chosen. Each entry says
what was decided and why.

## Routing

1. **Target priority is the position in the list.** Drag-and-drop edits it; there is no
   separate numeric priority field to keep in sync.
2. **Every strategy yields a full failover order.** `priority` uses list order,
   `round_robin` rotates the start, `weighted` draws a weighted-random order without
   replacement, `random` shuffles. After the first target fails, the remaining ones are
   tried in that order (bounded by the retry policy).
3. **Weights are integers >= 1.**
4. **Keys whose proxy is disabled are skipped**, both for a specific proxy and an inherited
   provider default. Switchyard never silently falls back to a direct connection.
5. **402 Payment Required is handled like 401/403** (key disabled, next key tried): it is
   key-specific (no credit left on that key).
6. **408 from an upstream counts as a timeout.** All other 4xx are client errors: no retry,
   error returned to the client.
7. **No cooldown after 5xx / timeout / connection errors** (the spec marks it optional).
   Only 429 cools a key down; 401/402/403 disable it.
8. **Final error status** when all attempts fail: the last upstream status for 429 and
   5xx, 504 for a timeout, 502 for connection errors or exhausted auth failures (a client
   must never see a 401 caused by an upstream key), 503 when no target/key was available.
9. **Retry-After** accepts seconds or an HTTP date; cooldowns are capped at 24 hours.
10. **Two timeouts.** Model *attempt timeout* (default 120 s): time allowed until the
    response is committed to the client (whole non-streaming exchange, or the first stream
    event). Provider *request timeout* (default 600 s): cap on the entire upstream request
    including streaming.
11. **"Reset" re-activates a key**: it clears a cooldown and an automatic disable.
12. **Least-recently-used** also counts in-flight picks (kept in memory) so concurrent
    requests do not all pick the same key.

## Protocols

13. **Same-format passthrough**: the request body is forwarded with only `model` replaced
    and response bytes are relayed unchanged. Usage is read on the side. For OpenAI
    streams, usage is only available when the client sets `stream_options.include_usage`;
    Switchyard does not inject it (that would change the stream the client sees).
14. **Translation drops fields without an equivalent** (e.g. `response_format`,
    `logprobs`, `n`, `thinking`, `cache_control`, `top_k`, server tools). Reasoning/thinking
    output is not translated. Images inside tool results become the text `[image omitted]`.
15. **Max tokens**: Anthropic requires `max_tokens`, so 8192 is used when the client sent
    none. Requests translated to OpenAI use `max_completion_tokens` (required by current
    OpenAI models, accepted by most compatible servers).
16. **Gemini tools** are sent as `parametersJsonSchema` (full JSON Schema, no lossy
    conversion). Function calls in the history carry the documented
    `skip_thought_signature_validator` signature, because real thought signatures cannot
    round-trip through OpenAI/Anthropic clients.
17. **Remote image URLs** are passed to Gemini as `fileData`; Gemini may reject non-Google
    URLs, so clients should send base64 data URLs for Gemini targets.
18. **Tool call IDs** sent to Anthropic are sanitised to `[A-Za-z0-9_-]` (IDs created by
    other providers may contain characters Anthropic rejects).
19. **The response `model` field is relayed from the upstream**, not rewritten.
20. **Upstream client errors** (e.g. 400) are relayed verbatim in passthrough mode; in
    translation mode the upstream message is wrapped in the inbound protocol's format.
21. **Base URL conventions follow each official SDK**: openai includes `/v1`
    (`https://api.openai.com/v1`), anthropic is the host (`https://api.anthropic.com`),
    gemini is the host (`https://generativelanguage.googleapis.com`). The GUI shows the
    resulting endpoint URL.
22. **`anthropic-version` / `anthropic-beta`** from the client are forwarded to
    anthropic-type upstreams (required by Claude Code).
23. **`/v1/messages/count_tokens` is not implemented.** Claude Code documents it as optional
    and falls back to a local estimate.
24. **`GET /v1/models` answers in Anthropic format** when the request carries an
    `anthropic-version` header, otherwise in OpenAI format.

## Non-chat endpoints

25. **Non-chat models only target openai-type providers.** For Gemini embeddings/images,
    add Gemini's OpenAI-compatible endpoint
    (`https://generativelanguage.googleapis.com/v1beta/openai`) as an openai provider.
26. **Videos**: OpenAI retired its own Videos API on 2026-09-24, but compatible providers
    keep the OpenAI-style shape, so `/v1/videos` is a generic passthrough.
    `GET /v1/videos/{id}/content` is also proxied (otherwise the result cannot be
    downloaded). Only the creating `POST` is logged; status polls and downloads are not.

## Security and admin

27. **`ADMIN_PASSWORD` only initialises the password.** Afterwards it is changed in
    Settings. `switchyard reset-password` re-applies `ADMIN_PASSWORD` if it is forgotten.
    Hashing: PBKDF2-SHA256 (standard library).
28. **Login rate limit**: 5 failed attempts per IP per 15 minutes, keyed on the TCP peer
    address. Behind a reverse proxy this becomes a global limit, which is fine for a
    single user.
29. **Sessions are stored in SQLite** (survive restarts), expire after 30 days, and
    changing the password signs out all other sessions.
30. **Encryption key**: AES-256 key derived from `ENCRYPTION_KEY` with HKDF-SHA256. A check
    value stored at first start makes Switchyard refuse to start with a different key
    instead of failing later on every request.
31. **Bulk key import skips duplicates** using an HMAC fingerprint of the secret (secrets
    are encrypted with random nonces, so they cannot be compared directly).
32. **Gateway keys** look like `sy-` + 48 hex characters and are stored as SHA-256 hashes.
33. **Deleting a proxy** switches keys that used it to "inherit" and clears it as a provider
    default. The confirmation dialog shows how many keys and providers are affected.

## Logs and GUI

34. **Requests rejected by gateway auth are not logged** (avoids log spam); everything after
    authentication is.
35. **Body logging** stores text bodies only, truncated to the configured size; binary and
    multipart bodies are replaced by a short placeholder.
36. **"Today" on the dashboard** starts at the browser's local midnight.
37. **Proxy test URL** is a global setting (Settings page). Test results are shown but not
    stored.
38. **Model "Test"** sends a small real request for chat, embedding, image, TTS and ASR
    (ASR uses a generated second of silence). Video models cannot be tested: generation is
    slow and expensive.
39. **No chart or drag-and-drop libraries**: charts are simple CSS bars; target reordering
    uses native HTML5 drag-and-drop plus up/down buttons.
