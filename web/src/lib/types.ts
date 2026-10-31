// Types of the admin JSON API (/api/*). Timestamps are ISO-8601 strings.

export type ProviderType = 'openai' | 'anthropic' | 'gemini'
export type KeyStrategy = 'round_robin' | 'random' | 'least_recently_used'

/** "off" = enabled:false (not counted in the other buckets). */
export interface KeyCounts {
  total: number
  active: number
  cooling_down: number
  disabled: number
  off: number
}

export interface Provider {
  id: number
  name: string
  type: ProviderType
  base_url: string
  headers: Record<string, string>
  timeout_seconds: number
  enabled: boolean
  proxy_id: number | null
  proxy_name: string
  key_strategy: KeyStrategy
  /** Minutes until keys disabled by 401/402/403 are re-activated; null = Settings default, 0 = never. */
  disabled_key_recovery_minutes: number | null
  created_at: string
  keys: KeyCounts
  model_count: number
}

export interface ProviderInput {
  name: string
  type: ProviderType
  base_url: string
  headers: Record<string, string>
  timeout_seconds: number
  enabled: boolean
  proxy_id: number | null
  key_strategy: KeyStrategy
  disabled_key_recovery_minutes: number | null
}

export type ProxyMode = 'inherit' | 'direct' | 'specific'
export type KeyStatus = 'active' | 'cooling_down' | 'disabled'

export interface ApiKey {
  id: number
  provider_id: number
  provider_name: string
  label: string
  secret_hint: string
  enabled: boolean
  proxy_mode: ProxyMode
  proxy_id: number | null
  /** null = direct connection */
  effective_proxy: { id: number; name: string; enabled: boolean } | null
  status: KeyStatus
  cooldown_until: string | null
  /** Set while a disabled key waits for automatic re-activation. */
  disabled_until: string | null
  last_error: string
  last_used_at: string | null
  success_count: number
  failure_count: number
  created_at: string
}

export interface KeyInput {
  label: string
  enabled: boolean
  proxy_mode: ProxyMode
  proxy_id: number | null
}

export interface BulkKeysResult {
  added: number
  skipped: number
  errors: { line: number; error: string }[]
}

export type KeyBatchAction = 'enable' | 'disable' | 'reset' | 'delete'

export interface TestResult {
  ok: boolean
  /** 0 = no response */
  status_code: number
  latency_ms: number
  error: string
}

export type ProxyType = 'http' | 'https' | 'socks5'

export interface Proxy {
  id: number
  name: string
  type: ProxyType
  host: string
  port: number
  username: string
  has_password: boolean
  enabled: boolean
  created_at: string
  used_by_keys: number
  used_by_providers: number
}

export interface ProxyInput {
  name: string
  type: ProxyType
  host: string
  port: number
  username: string
  /** Omit to keep the current password. */
  password?: string
  enabled: boolean
}

export interface ImportLine {
  line: number
  input: string
  status: 'valid' | 'duplicate' | 'invalid'
  error: string
  type: string
  host: string
  port: number
  username: string
}

export interface ImportResult {
  lines: ImportLine[]
  valid: number
  duplicates: number
  invalid: number
  created: number
}

export type ModelType = 'chat' | 'embedding' | 'image' | 'video' | 'tts' | 'asr'
export type Strategy = 'priority' | 'round_robin' | 'weighted' | 'random'
export type RetryOn = '429' | '5xx' | 'timeout' | 'connection'

export interface Target {
  id: number
  provider_id: number
  provider_name: string
  provider_type: ProviderType
  provider_enabled: boolean
  upstream_model: string
  weight: number
  enabled: boolean
}

export interface Model {
  id: number
  name: string
  type: ModelType
  strategy: Strategy
  max_total_attempts: number
  max_keys_per_target: number
  retry_on: RetryOn[]
  attempt_timeout_seconds: number
  vision_bridge_enabled: boolean
  vision_bridge_model_id: number | null
  vision_bridge_prompt: string
  created_at: string
  /** In priority order. */
  targets: Target[]
}

export interface ModelInput {
  name: string
  type: ModelType
  strategy: Strategy
  max_total_attempts: number
  max_keys_per_target: number
  retry_on: RetryOn[]
  attempt_timeout_seconds: number
  vision_bridge_enabled: boolean
  vision_bridge_model_id: number | null
  /** Empty = default prompt. */
  vision_bridge_prompt: string
  targets: { provider_id: number; upstream_model: string; weight: number; enabled: boolean }[]
}

export interface Attempt {
  provider_name: string
  key_hint: string
  upstream_model: string
  status_code: number
  latency_ms: number
  error: string
  /** Set on vision bridge calls (not upstream attempts): the model that described an image. */
  vision_bridge?: string
}

export interface ModelTestResult {
  ok: boolean
  status_code: number
  latency_ms: number
  output: string
  error: string
  attempts: Attempt[]
}

export interface LogEntry {
  id: number
  created_at: string
  endpoint: string
  model: string
  model_type: string
  provider_name: string
  key_hint: string
  upstream_model: string
  status_code: number
  latency_ms: number
  stream: boolean
  input_tokens: number | null
  output_tokens: number | null
  attempts: number
  error: string
}

export interface LogDetail extends LogEntry {
  attempt_details: Attempt[]
  request_body: string | null
  response_body: string | null
}

export interface LogPage {
  items: LogEntry[]
  next_before_id: number | null
}

export interface CountRow {
  name: string
  requests: number
  errors: number
}

export interface Dashboard {
  requests: number
  errors: number
  /** 0..1 */
  success_rate: number
  avg_latency_ms: number
  input_tokens: number
  output_tokens: number
  /** hour = 0-based hours since `since` */
  hourly: { hour: number; requests: number; errors: number }[]
  by_model: CountRow[]
  by_provider: CountRow[]
  keys: KeyCounts
  unhealthy_keys: ApiKey[]
  setup: { providers: number; keys: number; models: number; gateway_keys: number }
}

export interface Settings {
  log_retention_days: number
  default_cooldown_seconds: number
  /** 0 = never re-activate automatically. */
  disabled_key_recovery_minutes: number
  log_bodies: boolean
  log_body_max_bytes: number
  proxy_test_url: string
}

export interface GatewayKey {
  id: number
  name: string
  secret_hint: string
  enabled: boolean
  created_at: string
}

export interface CreatedGatewayKey extends GatewayKey {
  secret: string
}

export interface Me {
  version: string
}
