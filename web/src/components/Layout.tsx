import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import {
  Boxes,
  LayoutDashboard,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Network,
  ScrollText,
  Server,
  Settings,
  Sun,
  type LucideIcon,
} from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { Tip } from '@/components/Tip'
import { api, ApiError, errorMessage } from '@/lib/api'
import { useMe } from '@/lib/queries'
import { useTheme, type Theme } from '@/lib/theme'
import { cn } from '@/lib/utils'

const nav: { to: string; label: string; icon: LucideIcon }[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/providers', label: 'Providers', icon: Server },
  { to: '/proxies', label: 'Proxies', icon: Network },
  { to: '/models', label: 'Models', icon: Boxes },
  { to: '/logs', label: 'Logs', icon: ScrollText },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={className}>
      <rect width="32" height="32" rx="7" className="fill-primary" />
      <path
        d="M6 16h7l6-7h7M13 16l6 7h7"
        fill="none"
        className="stroke-primary-foreground"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function Brand() {
  return (
    <span className="flex items-center gap-2 font-semibold tracking-tight">
      <Logo className="size-6" />
      Switchyard
    </span>
  )
}

function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav className="grid gap-0.5">
      {nav.map(({ to, label, icon: Icon }) => (
        <NavLink
          key={to}
          to={to}
          end={to === '/'}
          onClick={onNavigate}
          className={({ isActive }) =>
            cn(
              'flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm font-medium transition-colors',
              isActive
                ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground',
            )
          }
        >
          <Icon className="size-4" />
          {label}
        </NavLink>
      ))}
    </nav>
  )
}

const themes: { value: Theme; label: string; icon: LucideIcon }[] = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
]

function ThemeSwitcher() {
  const { theme, setTheme } = useTheme()
  return (
    <div role="radiogroup" aria-label="Theme" className="inline-flex rounded-md border bg-background p-0.5">
      {themes.map(({ value, label, icon: Icon }) => (
        <Tip key={value} content={`${label} theme`}>
          <button
            type="button"
            role="radio"
            aria-checked={theme === value}
            aria-label={`${label} theme`}
            onClick={() => setTheme(value)}
            className={cn(
              'rounded-sm p-1.5 text-muted-foreground transition-colors hover:text-foreground',
              theme === value && 'bg-accent text-foreground',
            )}
          >
            <Icon className="size-3.5" />
          </button>
        </Tip>
      ))}
    </div>
  )
}

function SidebarFooter() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const me = useMe()
  const [pending, setPending] = useState(false)

  async function logout() {
    setPending(true)
    try {
      await api.post('/api/auth/logout')
    } catch (err) {
      // 401 = the session is already gone, which is what we want.
      if (!(err instanceof ApiError && err.status === 401)) {
        toast.error(errorMessage(err))
        setPending(false)
        return
      }
    }
    queryClient.clear()
    navigate('/login', { replace: true })
  }

  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <ThemeSwitcher />
        <Button variant="ghost" size="sm" onClick={logout} disabled={pending} className="text-muted-foreground">
          <LogOut />
          Log out
        </Button>
      </div>
      {me.data?.version && <p className="px-1 text-xs text-muted-foreground">Version {me.data.version}</p>}
    </div>
  )
}

export default function Layout() {
  const [menuOpen, setMenuOpen] = useState(false)

  return (
    <div className="min-h-svh">
      <aside className="fixed inset-y-0 left-0 hidden w-[232px] flex-col border-r bg-sidebar text-sidebar-foreground md:flex">
        <div className="flex h-14 items-center px-5">
          <Brand />
        </div>
        <div className="flex-1 overflow-y-auto px-3 py-2">
          <NavLinks />
        </div>
        <div className="border-t p-3">
          <SidebarFooter />
        </div>
      </aside>

      <header className="sticky top-0 z-40 flex h-14 items-center gap-2 border-b bg-background/95 px-3 backdrop-blur md:hidden">
        <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
          <SheetTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Open menu">
              <Menu />
            </Button>
          </SheetTrigger>
          <SheetContent side="left" className="w-[260px] gap-0 bg-sidebar p-0">
            <SheetHeader className="h-14 justify-center border-b px-5 py-0">
              <SheetTitle>
                <Brand />
              </SheetTitle>
              <SheetDescription className="sr-only">Navigation</SheetDescription>
            </SheetHeader>
            <div className="flex-1 overflow-y-auto p-3">
              <NavLinks onNavigate={() => setMenuOpen(false)} />
            </div>
            <div className="border-t p-3">
              <SidebarFooter />
            </div>
          </SheetContent>
        </Sheet>
        <Brand />
      </header>

      <main className="md:pl-[232px]">
        <div className="mx-auto w-full max-w-[1400px] p-4 sm:p-6 lg:p-8">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
