import { useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Field } from '@/components/common'
import { Logo } from '@/components/Layout'
import { api, ApiError, errorMessage } from '@/lib/api'

export default function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  const from = (location.state as { from?: string } | null)?.from
  const target = from && !from.startsWith('/login') ? from : '/'

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!password) {
      setError('Enter the admin password.')
      return
    }
    setPending(true)
    setError('')
    try {
      await api.post('/api/auth/login', { password })
      queryClient.clear()
      navigate(target, { replace: true })
    } catch (err) {
      // 429 carries the API's message, which says when to try again.
      setError(err instanceof ApiError && err.status === 401 ? 'Wrong password' : errorMessage(err))
      setPending(false)
    }
  }

  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/40 p-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="justify-items-center text-center">
          <Logo className="mb-2 size-10" />
          <CardTitle className="text-xl">Switchyard</CardTitle>
          <CardDescription>Sign in to manage your AI gateway.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="grid gap-4" noValidate>
            <Field label="Admin password" htmlFor="password" error={error}>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                autoFocus
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                aria-invalid={!!error}
              />
            </Field>
            <Button type="submit" disabled={pending}>
              {pending ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
