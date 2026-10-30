import type { ApiKey, KeyStrategy, ModelType, ProviderType, ProxyType, RetryOn, Strategy } from './types'

// User-facing names and one-line explanations for enum values.

export const providerTypes: { value: ProviderType; label: string; short: string }[] = [
  { value: 'openai', label: 'OpenAI-compatible', short: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic-compatible', short: 'Anthropic' },
  { value: 'gemini', label: 'Gemini-compatible', short: 'Gemini' },
]

export const defaultBaseUrls: Record<ProviderType, string> = {
  openai: 'https://api.openai.com/v1',
  anthropic: 'https://api.anthropic.com',
  gemini: 'https://generativelanguage.googleapis.com',
}

/** The chat URL Switchyard calls for a provider, shown as a hint under the base URL. */
export function chatEndpoint(type: ProviderType, baseUrl: string): string {
  const base = baseUrl.trim().replace(/\/+$/, '')
  if (!base) return ''
  if (type === 'openai') return `${base}/chat/completions`
  if (type === 'anthropic') return `${base}/v1/messages`
  const hasVersion = /\/v\d+[a-z0-9]*$/i.test(base) // e.g. /v1 or /v1beta
  return `${base}${hasVersion ? '' : '/v1beta'}/models/{model}:generateContent`
}

export const keyStrategies: { value: KeyStrategy; label: string; help: string }[] = [
  { value: 'round_robin', label: 'Round robin', help: 'Use the keys in turn, one after another.' },
  { value: 'random', label: 'Random', help: 'Pick a random key for each request.' },
  { value: 'least_recently_used', label: 'Least recently used', help: 'Pick the key that has been idle the longest.' },
]

export const proxyTypes: { value: ProxyType; label: string }[] = [
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
  { value: 'socks5', label: 'SOCKS5' },
]

export const modelTypes: { value: ModelType; label: string; endpoint: string }[] = [
  { value: 'chat', label: 'Chat', endpoint: '/v1/chat/completions, /v1/messages' },
  { value: 'embedding', label: 'Embedding', endpoint: '/v1/embeddings' },
  { value: 'image', label: 'Image', endpoint: '/v1/images/generations' },
  { value: 'video', label: 'Video', endpoint: '/v1/videos' },
  { value: 'tts', label: 'TTS', endpoint: '/v1/audio/speech' },
  { value: 'asr', label: 'ASR', endpoint: '/v1/audio/transcriptions' },
]

export const strategies: { value: Strategy; label: string; help: string }[] = [
  { value: 'priority', label: 'Priority', help: 'Try targets top to bottom (failover).' },
  { value: 'round_robin', label: 'Round robin', help: 'Rotate which target goes first.' },
  { value: 'weighted', label: 'Weighted', help: 'Pick the first target randomly by weight.' },
  { value: 'random', label: 'Random', help: 'Pick the first target randomly.' },
]

export const retryOnOptions: { value: RetryOn; label: string }[] = [
  { value: '429', label: '429 Rate limited' },
  { value: '5xx', label: '5xx Server error' },
  { value: 'timeout', label: 'Timeout' },
  { value: 'connection', label: 'Connection error' },
]

export function labelOf<T extends string>(list: { value: T; label: string }[], value: T): string {
  return list.find((item) => item.value === value)?.label ?? value
}

/** How a key connects: "Inherit → us-1", "Inherit (direct)", "Direct" or the proxy name. */
export function keyProxyText(key: ApiKey): string {
  const proxy = key.effective_proxy
  if (key.proxy_mode === 'inherit') return proxy ? `Inherit → ${proxy.name}` : 'Inherit (direct)'
  if (key.proxy_mode === 'direct') return 'Direct'
  return proxy?.name ?? 'Missing proxy'
}
