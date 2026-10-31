# Switchyard

A small, self-hosted AI API gateway for one user. Your clients call Switchyard with **one
gateway key**; Switchyard routes each request to your upstream providers and API keys
according to rules you configure in a web GUI.

- OpenAI-compatible API (`/v1/chat/completions`, embeddings, images, audio, videos, models)
- Anthropic-compatible API (`/v1/messages`), so Claude Code and other Anthropic clients work
- Upstream providers of type **openai**, **anthropic** or **gemini** (any compatible base URL);
  chat requests are translated between formats, including streaming, images and tool calls
- Per model: ordered targets (drag to reorder), routing strategy, retry policy
- Per provider: many keys, key selection strategy, automatic cooldown on 429 and disabling on
  401/402/403 (optionally re-activated after a recovery period)
- Vision bridge: chat models without vision accept images, which a vision model describes first
- HTTP, HTTPS and SOCKS5 proxies per key or per provider, bulk import and bulk assignment
- Request logs with per-attempt details, a dashboard, light and dark mode
- One binary (GUI embedded), one SQLite file, secrets encrypted at rest

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for how it works and
[docs/DECISIONS.md](docs/DECISIONS.md) for the choices made where the spec was open.

## Run it on Ubuntu with Docker

1. Install Docker Engine with the Compose plugin
   ([official guide](https://docs.docker.com/engine/install/ubuntu/)), for example:

   ```bash
   sudo apt-get update && sudo apt-get install -y ca-certificates curl git openssl
   curl -fsSL https://get.docker.com | sudo sh
   sudo usermod -aG docker "$USER"   # then log out and back in
   ```

2. Get the code and create the configuration:

   ```bash
   git clone https://github.com/MaziarAshtari/Switchyard.git
   cd Switchyard
   cp .env.example .env
   sed -i "s/^ENCRYPTION_KEY=.*/ENCRYPTION_KEY=$(openssl rand -hex 32)/" .env
   nano .env   # set ADMIN_PASSWORD (at least 8 characters)
   ```

3. Start it:

   ```bash
   docker compose up -d --build
   docker compose logs -f        # "switchyard listening" means it is ready
   ```

4. Open `http://<server>:8080`, sign in with `ADMIN_PASSWORD`, then:
   **Providers** → add a provider and its API keys → **Models** → create a public model with
   one or more targets → **Settings** → create a gateway key for your clients.

Data lives in the Docker volume `switchyard-data` (mounted at `/data`).
Upgrade with `git pull && docker compose up -d --build`.

> Put Switchyard behind a reverse proxy with TLS if it is reachable from the internet, and
> publish the port only on localhost (`"127.0.0.1:8080:8080"` in `docker-compose.yml`).
> Example with [Caddy](https://caddyserver.com): `gateway.example.com { reverse_proxy localhost:8080 }`.
> With nginx, disable buffering so streams are not delayed: `proxy_buffering off;`.

## Configuration

| Variable         | Default  | Description |
|------------------|----------|-------------|
| `PORT`           | `8080`   | HTTP port. |
| `DATA_DIR`       | `./data` | Directory of `switchyard.db` (always `/data` in Docker). |
| `ADMIN_PASSWORD` | –        | Initial GUI password, required on first start (min. 8 characters). Later changes are made in Settings. |
| `ENCRYPTION_KEY` | –        | Required, min. 16 characters. Encrypts upstream API keys and proxy passwords (AES-GCM). Never change it after the first start. |

Everything else (log retention, default cooldown, disabled key recovery, body logging, proxy test
URL) is in **Settings**.

**Forgot the password?** Set a new one with:

```bash
docker compose run --rm -e ADMIN_PASSWORD='new-password-here' switchyard reset-password
```

## Using the API

Base URL: `http://<server>:8080/v1`. Authenticate with a gateway key (created in Settings) as
`Authorization: Bearer <key>`, `x-api-key: <key>` or `x-goog-api-key: <key>`.
The `model` field is always one of your **public model names**; a model's type must match the
endpoint (a `tts` model only works on `/v1/audio/speech`).

```bash
export SY=http://localhost:8080
export KEY=sy-your-gateway-key
```

Health check (no auth):

```bash
curl $SY/healthz
```

List models:

```bash
curl $SY/v1/models -H "Authorization: Bearer $KEY"
```

Chat (OpenAI format), add `"stream": true` for server-sent events:

```bash
curl $SY/v1/chat/completions -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{
  "model": "gpt-best",
  "messages": [{"role": "user", "content": "Hello!"}]
}'

curl -N $SY/v1/chat/completions -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{
  "model": "gpt-best", "stream": true, "stream_options": {"include_usage": true},
  "messages": [{"role": "user", "content": "Count to five"}]
}'
```

Chat (Anthropic format):

```bash
curl $SY/v1/messages -H "x-api-key: $KEY" -H "anthropic-version: 2023-06-01" -H "Content-Type: application/json" -d '{
  "model": "claude-best", "max_tokens": 512,
  "messages": [{"role": "user", "content": "Hello!"}]
}'
```

Embeddings:

```bash
curl $SY/v1/embeddings -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{
  "model": "my-embeddings", "input": "The quick brown fox"
}'
```

Images:

```bash
curl $SY/v1/images/generations -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{
  "model": "my-images", "prompt": "A lighthouse at dawn", "size": "1024x1024"
}'
```

Text to speech:

```bash
curl $SY/v1/audio/speech -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{
  "model": "my-tts", "input": "Hello from Switchyard", "voice": "alloy"
}' --output speech.mp3
```

Speech to text (multipart):

```bash
curl $SY/v1/audio/transcriptions -H "Authorization: Bearer $KEY" \
  -F model=my-asr -F file=@speech.mp3
```

Video (OpenAI-style async jobs; polling always goes to the key that created the job):

```bash
curl $SY/v1/videos -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{
  "model": "my-video", "prompt": "A paper boat on a river", "seconds": "4"
}'
curl $SY/v1/videos/VIDEO_ID -H "Authorization: Bearer $KEY"
curl $SY/v1/videos/VIDEO_ID/content -H "Authorization: Bearer $KEY" --output video.mp4
```

Errors use the format of the protocol you called (OpenAI-style `{"error": {...}}`, or
Anthropic-style `{"type": "error", ...}` on `/v1/messages`).

## Client configuration

**OpenAI SDK (Python)**

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="sy-your-gateway-key")
reply = client.chat.completions.create(
    model="gpt-best",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(reply.choices[0].message.content)
```

**OpenAI SDK (Node.js)**

```js
import OpenAI from "openai";

const client = new OpenAI({ baseURL: "http://localhost:8080/v1", apiKey: "sy-your-gateway-key" });
const reply = await client.chat.completions.create({
  model: "gpt-best",
  messages: [{ role: "user", content: "Hello!" }],
});
console.log(reply.choices[0].message.content);
```

**Claude Code**

Claude Code talks to `/v1/messages`. Point it at Switchyard and map its model slots to your
public models (any target type works; non-Anthropic targets are translated):

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080      # no /v1
export ANTHROPIC_AUTH_TOKEN=sy-your-gateway-key
export ANTHROPIC_DEFAULT_OPUS_MODEL=claude-best
export ANTHROPIC_DEFAULT_SONNET_MODEL=claude-best
export ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-fast
claude
```

or the same values in `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:8080",
    "ANTHROPIC_AUTH_TOKEN": "sy-your-gateway-key",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-best",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-fast"
  }
}
```

Alternatively name your public models exactly like the Claude model IDs Claude Code requests.
`anthropic-beta` and `anthropic-version` headers are forwarded to Anthropic-type providers.

## Routing in one paragraph

A public model has an ordered list of targets (provider + upstream model). The model's
strategy decides which target goes first (priority, round robin, weighted, random); the
others follow as failover. Within a target, the provider's key strategy picks keys (round
robin, random, least recently used), skipping disabled keys, keys cooling down and keys whose
proxy is disabled. A 429 cools the key down (Retry-After or the default cooldown), 401/402/403
disable it (until reset, or until the recovery period has passed), 5xx/timeouts/connection errors move on, other 4xx errors go straight back to the
client. `max_keys_per_target` and `max_total_attempts` bound the attempts. Streams are retried
only before the first byte reaches the client.

## Supported, and not

- Chat translation works between OpenAI and Anthropic clients and openai/anthropic/gemini
  providers. Fields without an equivalent are dropped (see DECISIONS.md).
- Embeddings, images, audio and video are passed through to **openai-type** providers only.
  For Gemini embeddings or images, add Gemini's OpenAI-compatible endpoint
  (`https://generativelanguage.googleapis.com/v1beta/openai`) as an openai provider.
- `/v1/messages/count_tokens` is not implemented (Claude Code falls back to an estimate).

## Development

Requirements: Go 1.27+, Node.js 24+ with pnpm 10.

```bash
make build      # GUI + binary -> bin/switchyard
make run        # build and run with the values from .env
make test       # go test ./...
make check      # vet, tests, GUI lint and build
make dev-web    # Vite dev server on :5173, proxying /api and /v1 to :8080
```

Project layout: `cmd/switchyard` (main), `internal/*` (packages described in
docs/ARCHITECTURE.md), `web/` (React GUI, embedded from `web/dist`).
