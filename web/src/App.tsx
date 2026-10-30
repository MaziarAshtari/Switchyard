import { useEffect, type ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import Layout from '@/components/Layout'
import { ErrorState } from '@/components/common'
import { ApiError, setUnauthorizedHandler } from '@/lib/api'
import { useMe } from '@/lib/queries'
import DashboardPage from '@/pages/DashboardPage'
import LoginPage from '@/pages/LoginPage'
import LogsPage from '@/pages/logs/LogsPage'
import ModelEditorPage from '@/pages/models/ModelEditorPage'
import ModelsPage from '@/pages/models/ModelsPage'
import ProviderDetailPage from '@/pages/providers/ProviderDetailPage'
import ProvidersPage from '@/pages/providers/ProvidersPage'
import ProxiesPage from '@/pages/proxies/ProxiesPage'
import SettingsPage from '@/pages/settings/SettingsPage'

export default function App() {
  useSessionExpiry()

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<DashboardPage />} />
        <Route path="providers" element={<ProvidersPage />} />
        <Route path="providers/:id" element={<ProviderDetailPage />} />
        <Route path="proxies" element={<ProxiesPage />} />
        <Route path="models" element={<ModelsPage />} />
        <Route path="models/new" element={<ModelEditorPage />} />
        <Route path="models/:id" element={<ModelEditorPage />} />
        <Route path="logs" element={<LogsPage />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

/** When any API call reports an expired session, drop all cached data and go to /login. */
function useSessionExpiry() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  useEffect(() => {
    setUnauthorizedHandler(() => {
      const { pathname, search } = window.location
      if (pathname === '/login') return
      // Navigate first so protected pages unmount instead of refetching the cleared queries.
      navigate('/login', { replace: true, state: { from: pathname + search } })
      queryClient.clear()
    })
  }, [navigate, queryClient])
}

function RequireAuth({ children }: { children: ReactNode }) {
  const location = useLocation()
  const me = useMe()

  if (me.isPending) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <Loader2 className="size-5 animate-spin text-muted-foreground" aria-label="Loading" />
      </div>
    )
  }
  if (me.isError) {
    if (me.error instanceof ApiError && me.error.status === 401) {
      return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
    }
    return (
      <div className="flex min-h-svh items-center justify-center p-4">
        <div className="w-full max-w-md">
          <ErrorState error={me.error} onRetry={() => me.refetch()} />
        </div>
      </div>
    )
  }
  return children
}
