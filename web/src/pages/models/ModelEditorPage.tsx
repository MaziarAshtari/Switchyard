import { useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Zap } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { EmptyState, ErrorState, Field, FormError, Loading, PageHeader } from '@/components/common'
import { ConfirmDialog, type ConfirmOptions } from '@/components/ConfirmDialog'
import { Tip } from '@/components/Tip'
import { api, ApiError } from '@/lib/api'
import { intInRange, toErrors, useFormState, type Errors } from '@/lib/form'
import { modelTypes, retryOnOptions, strategies } from '@/lib/labels'
import { useAction, useModels, useModelTest, useProviders } from '@/lib/queries'
import type { Model, ModelInput, ModelType, Provider, RetryOn, Strategy } from '@/lib/types'
import { cn } from '@/lib/utils'
import { TestOutcome } from './ModelTest'
import { TargetsEditor, type TargetRow } from './TargetsEditor'

const back = { to: '/models', label: 'Models' }
const allRetryOn: RetryOn[] = ['429', '5xx', 'timeout', 'connection']
const maxAttempts = 200
const maxPromptLength = 4000
// Same text as store.DefaultVisionBridgePrompt.
const defaultVisionPrompt =
  "Describe this image in detail, including all visible text, objects, layout, and relevant context so another AI without vision can answer the user's request accurately."
let nextRowKey = 1

export default function ModelEditorPage() {
  const { id } = useParams()
  // Remount when switching models (e.g. right after creating one) so the form starts fresh.
  return <ModelEditor key={id ?? 'new'} id={id === undefined ? null : Number(id)} />
}

function ModelEditor({ id }: { id: number | null }) {
  const model = useQuery({
    queryKey: ['models', id],
    queryFn: () => api.get<Model>(`/api/models/${id}`),
    enabled: id !== null,
  })

  if (id !== null && model.isPending) {
    return (
      <>
        <PageHeader back={back} title="Model" />
        <Loading rows={6} />
      </>
    )
  }
  if (id !== null && model.isError) {
    const notFound = model.error instanceof ApiError && model.error.status === 404
    return (
      <>
        <PageHeader back={back} title="Model" />
        {notFound ? (
          <EmptyState
            title="Model not found"
            hint="It may have been deleted."
            action={
              <Button asChild variant="outline">
                <Link to="/models">All models</Link>
              </Button>
            }
          />
        ) : (
          <ErrorState error={model.error} onRetry={model.refetch} />
        )}
      </>
    )
  }
  return <ModelForm model={model.data} />
}

interface FormValues {
  name: string
  type: ModelType
  strategy: Strategy
  max_total_attempts: string
  max_keys_per_target: string
  retry_on: RetryOn[]
  attempt_timeout_seconds: string
  vision_bridge_enabled: boolean
  vision_bridge_model_id: number | null
  vision_bridge_prompt: string
  targets: TargetRow[]
}

function initialValues(m?: Model): FormValues {
  return {
    name: m?.name ?? '',
    type: m?.type ?? 'chat',
    strategy: m?.strategy ?? 'priority',
    max_total_attempts: String(m?.max_total_attempts ?? 3),
    max_keys_per_target: String(m?.max_keys_per_target ?? 2),
    retry_on: m?.retry_on ?? allRetryOn,
    attempt_timeout_seconds: String(m?.attempt_timeout_seconds ?? 120),
    vision_bridge_enabled: m?.vision_bridge_enabled ?? false,
    vision_bridge_model_id: m?.vision_bridge_model_id ?? null,
    vision_bridge_prompt: m?.vision_bridge_prompt ?? defaultVisionPrompt,
    targets: (m?.targets ?? []).map((t) => ({
      key: nextRowKey++,
      provider_id: t.provider_id,
      upstream_model: t.upstream_model,
      weight: String(t.weight),
      enabled: t.enabled,
    })),
  }
}

function validate(v: FormValues, providers: Provider[]): { input?: ModelInput; errors: Errors } {
  const errors: Errors = {}
  const name = v.name.trim()
  if (!name) errors.name = 'Name is required'
  else if (/\s/.test(name)) errors.name = 'Name must not contain spaces'
  const attempts = intInRange(v.max_total_attempts, 1, maxAttempts)
  if (attempts === null) errors.max_total_attempts = `Enter a number from 1 to ${maxAttempts}`
  const keysPerTarget = intInRange(v.max_keys_per_target, 1, maxAttempts)
  if (keysPerTarget === null) errors.max_keys_per_target = `Enter a number from 1 to ${maxAttempts}`
  const timeout = intInRange(v.attempt_timeout_seconds, 1, 3600)
  if (timeout === null) errors.attempt_timeout_seconds = 'Enter a number of seconds from 1 to 3600'
  if (v.targets.length === 0) errors.targets = 'Add at least one target'
  const chat = v.type === 'chat'
  const bridge = chat && v.vision_bridge_enabled
  if (bridge && v.vision_bridge_model_id === null) errors.vision_bridge_model_id = 'Choose the model that describes images'
  const prompt = v.vision_bridge_prompt.trim()
  if (chat && prompt.length > maxPromptLength) errors.vision_bridge_prompt = `Use at most ${maxPromptLength} characters`

  const targets = v.targets.map((t, i) => {
    const provider = providers.find((p) => p.id === t.provider_id)
    if (t.provider_id === null) errors[`targets.${i}.provider_id`] = 'Choose a provider'
    else if (provider && v.type !== 'chat' && provider.type !== 'openai') {
      errors[`targets.${i}.provider_id`] = 'Non-chat models can only use OpenAI-compatible providers'
    }
    if (!t.upstream_model.trim()) errors[`targets.${i}.upstream_model`] = 'Upstream model is required'
    let weight = intInRange(t.weight, 1, 1000)
    if (weight === null) {
      // Weights only matter for the weighted strategy; otherwise fall back to 1.
      if (v.strategy === 'weighted') errors[`targets.${i}.weight`] = 'Weight must be a whole number from 1 to 1000'
      weight = 1
    }
    return { provider_id: t.provider_id ?? 0, upstream_model: t.upstream_model.trim(), weight, enabled: t.enabled }
  })

  if (Object.keys(errors).length > 0 || attempts === null || keysPerTarget === null || timeout === null) {
    return { errors: { ...errors, _: 'Please fix the highlighted fields.' } }
  }
  return {
    errors,
    input: {
      name,
      type: v.type,
      strategy: v.strategy,
      max_total_attempts: attempts,
      max_keys_per_target: keysPerTarget,
      retry_on: v.retry_on,
      attempt_timeout_seconds: timeout,
      vision_bridge_enabled: bridge,
      vision_bridge_model_id: chat ? v.vision_bridge_model_id : null,
      vision_bridge_prompt: chat ? prompt : '',
      targets,
    },
  }
}

function ModelForm({ model }: { model?: Model }) {
  const navigate = useNavigate()
  const providers = useProviders()
  const models = useModels()
  const [values, set, setValues] = useFormState(() => initialValues(model))
  const [errors, setErrors] = useState<Errors>({})
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null)
  const { test, run } = useModelTest(setConfirm)
  const save = useAction(
    (input: ModelInput) => (model ? api.put<Model>(`/api/models/${model.id}`, input) : api.post<Model>('/api/models', input)),
    { success: model ? 'Model saved' : 'Model created', toastErrors: false },
  )
  const providerList = providers.data ?? []
  const typeInfo = modelTypes.find((t) => t.value === values.type)
  const visionChoices = (models.data ?? []).filter((m) => m.type === 'chat' && m.id !== model?.id)

  function submit(e: FormEvent) {
    e.preventDefault()
    const { input, errors } = validate(values, providerList)
    setErrors(errors)
    if (!input) return
    save.mutate(input, {
      onSuccess: (saved) => {
        if (model) setValues(initialValues(saved))
        else navigate(`/models/${saved.id}`, { replace: true })
      },
      onError: (err) => setErrors(toErrors(err)),
    })
  }

  function toggleRetry(value: RetryOn, checked: boolean) {
    set(
      'retry_on',
      allRetryOn.filter((r) => (r === value ? checked : values.retry_on.includes(r))),
    )
  }

  return (
    <form onSubmit={submit} noValidate>
      <PageHeader
        back={back}
        title={model ? <span className="font-mono">{model.name}</span> : 'New model'}
        description={model ? 'Edit routing, targets and retries for this model.' : 'A public model name and where it is routed.'}
        actions={model && <TestButton model={model} pending={test.isPending} onTest={() => run(model)} />}
      />

      {(test.isPending || test.data || test.error) && (
        <Card className="mb-4">
          <CardHeader>
            <CardTitle>Test result</CardTitle>
            <CardDescription>Uses the saved configuration.</CardDescription>
          </CardHeader>
          <CardContent>
            <TestOutcome pending={test.isPending} result={test.data} error={test.error} />
          </CardContent>
        </Card>
      )}

      <div className="grid gap-4">
        <Section title="General">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Public name" htmlFor="model-name" error={errors.name} help="Clients send this as the model name.">
              <Input
                id="model-name"
                value={values.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder="e.g. gpt-best"
                className="font-mono"
                aria-invalid={!!errors.name}
                autoFocus={!model}
              />
            </Field>
            <Field
              label="Type"
              htmlFor="model-type"
              error={errors.type}
              help={
                values.type === 'chat'
                  ? `Clients call ${typeInfo?.endpoint}. Any provider type works; requests are translated.`
                  : `Clients call ${typeInfo?.endpoint}. Non-chat models can only use OpenAI-compatible providers.`
              }
            >
              <Select value={values.type} onValueChange={(t) => set('type', t as ModelType)}>
                <SelectTrigger id="model-type" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {modelTypes.map((t) => (
                    <SelectItem key={t.value} value={t.value}>
                      {t.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
        </Section>

        <Section title="Targets" description="Provider models this model routes to. The order is the priority.">
          <TargetsEditor
            targets={values.targets}
            onChange={(targets) => set('targets', targets)}
            onAdd={() =>
              set('targets', [
                ...values.targets,
                { key: nextRowKey++, provider_id: null, upstream_model: '', weight: '1', enabled: true },
              ])
            }
            providers={providerList}
            modelType={values.type}
            strategy={values.strategy}
            errors={errors}
          />
        </Section>

        <Section title="Routing" description="Which target is tried first.">
          <div role="radiogroup" aria-label="Strategy" className="grid gap-2 sm:grid-cols-2">
            {strategies.map((s) => (
              <label
                key={s.value}
                className={cn(
                  'flex cursor-pointer items-start gap-3 rounded-md border p-3 text-sm transition-colors hover:bg-muted/40',
                  values.strategy === s.value && 'border-primary ring-1 ring-primary',
                )}
              >
                <input
                  type="radio"
                  name="strategy"
                  value={s.value}
                  checked={values.strategy === s.value}
                  onChange={() => set('strategy', s.value)}
                  className="mt-0.5 accent-primary"
                />
                <span>
                  <span className="font-medium">{s.label}</span>
                  <span className="block text-xs text-muted-foreground">{s.help}</span>
                </span>
              </label>
            ))}
          </div>
          {errors.strategy && <p className="mt-2 text-xs font-medium text-destructive">{errors.strategy}</p>}
          <p className="mt-3 text-xs text-muted-foreground">If a target fails, the remaining targets are tried in order.</p>
        </Section>

        <Section title="Retry policy" description="What happens when an upstream request fails.">
          <div className="grid gap-4">
            <div className="grid gap-4 sm:grid-cols-3">
              <Field
                label="Max total attempts"
                htmlFor="model-attempts"
                error={errors.max_total_attempts}
                help={`1–${maxAttempts}, across all targets and keys.`}
              >
                <Input
                  id="model-attempts"
                  inputMode="numeric"
                  value={values.max_total_attempts}
                  onChange={(e) => set('max_total_attempts', e.target.value)}
                  aria-invalid={!!errors.max_total_attempts}
                />
              </Field>
              <Field
                label="Max keys per target"
                htmlFor="model-keys"
                error={errors.max_keys_per_target}
                help={`1–${maxAttempts} keys tried before moving to the next target.`}
              >
                <Input
                  id="model-keys"
                  inputMode="numeric"
                  value={values.max_keys_per_target}
                  onChange={(e) => set('max_keys_per_target', e.target.value)}
                  aria-invalid={!!errors.max_keys_per_target}
                />
              </Field>
              <Field
                label="Attempt timeout (seconds)"
                htmlFor="model-timeout"
                error={errors.attempt_timeout_seconds}
                help="How long to wait for an upstream to respond before trying the next key."
              >
                <Input
                  id="model-timeout"
                  inputMode="numeric"
                  value={values.attempt_timeout_seconds}
                  onChange={(e) => set('attempt_timeout_seconds', e.target.value)}
                  aria-invalid={!!errors.attempt_timeout_seconds}
                />
              </Field>
            </div>
            <div className="grid gap-2">
              <Label>Retry on</Label>
              <div className="flex flex-wrap gap-x-6 gap-y-2">
                {retryOnOptions.map((o) => (
                  <label key={o.value} className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={values.retry_on.includes(o.value)}
                      onCheckedChange={(c) => toggleRetry(o.value, c === true)}
                    />
                    {o.label}
                  </label>
                ))}
              </div>
              {errors.retry_on && <p className="text-xs font-medium text-destructive">{errors.retry_on}</p>}
            </div>
            <p className="rounded-md bg-muted/60 px-3 py-2 text-xs text-muted-foreground">
              401/402/403 always disable the key and try the next one. Other 4xx errors are returned to the client.
            </p>
          </div>
        </Section>

        {values.type === 'chat' && (
          <Section title="Vision bridge" description="Lets this model accept images even if its targets cannot see them.">
            <div className="grid gap-4">
              <label className="flex items-center justify-between gap-4 rounded-md border p-3">
                <span className="grid gap-0.5">
                  <span className="text-sm font-medium">Describe images first</span>
                  <span className="text-xs text-muted-foreground">
                    Each image is sent to the vision model and replaced by its description before this model is called.
                  </span>
                </span>
                <Switch
                  checked={values.vision_bridge_enabled}
                  onCheckedChange={(c) => set('vision_bridge_enabled', c)}
                  aria-label="Vision bridge"
                />
              </label>
              <Field
                label="Vision model"
                htmlFor="model-vision"
                error={errors.vision_bridge_model_id}
                help={visionChoices.length ? 'Another chat model that accepts images.' : 'Create another chat model first.'}
              >
                <Select
                  value={values.vision_bridge_model_id === null ? '' : String(values.vision_bridge_model_id)}
                  onValueChange={(v) => set('vision_bridge_model_id', Number(v))}
                  disabled={!values.vision_bridge_enabled}
                >
                  <SelectTrigger
                    id="model-vision"
                    className="w-full sm:w-80"
                    aria-invalid={!!errors.vision_bridge_model_id}
                  >
                    <SelectValue placeholder="Choose a chat model" />
                  </SelectTrigger>
                  <SelectContent>
                    {visionChoices.map((m) => (
                      <SelectItem key={m.id} value={String(m.id)}>
                        <span className="font-mono">{m.name}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Prompt"
                htmlFor="model-vision-prompt"
                error={errors.vision_bridge_prompt}
                help="Sent with each image, together with the text of the user's message."
              >
                <Textarea
                  id="model-vision-prompt"
                  rows={3}
                  value={values.vision_bridge_prompt}
                  onChange={(e) => set('vision_bridge_prompt', e.target.value)}
                  disabled={!values.vision_bridge_enabled}
                  aria-invalid={!!errors.vision_bridge_prompt}
                />
              </Field>
            </div>
          </Section>
        )}
      </div>

      <div className="sticky bottom-0 z-10 -mx-4 mt-6 -mb-4 flex flex-wrap items-center justify-end gap-2 border-t bg-background/95 px-4 py-3 backdrop-blur sm:-mx-6 sm:-mb-6 sm:px-6 lg:-mx-8 lg:-mb-8 lg:px-8">
        <div className="mr-auto min-w-0">
          <FormError message={errors._} />
        </div>
        <Button type="button" variant="outline" asChild>
          <Link to="/models">Cancel</Link>
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? 'Saving…' : model ? 'Save' : 'Create model'}
        </Button>
      </div>

      <ConfirmDialog options={confirm} onClose={() => setConfirm(null)} />
    </form>
  )
}

function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <Card className="gap-4">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}

function TestButton({ model, pending, onTest }: { model: Model; pending: boolean; onTest: () => void }) {
  if (model.type === 'video') {
    return (
      <Tip content="Video models cannot be tested: generation is slow and expensive.">
        <span tabIndex={0}>
          <Button type="button" variant="outline" disabled>
            <Zap />
            Test
          </Button>
        </span>
      </Tip>
    )
  }
  return (
    <Tip content="Sends a sample request using the saved configuration.">
      <Button type="button" variant="outline" onClick={onTest} disabled={pending}>
        <Zap />
        {pending ? 'Testing…' : 'Test'}
      </Button>
    </Tip>
  )
}
