import { Link } from '@tanstack/react-router'
import { useAuth } from '@/auth/context'
import { cn } from '@/lib/utils'
import { NAV_ITEMS, SIDEBAR_PRIMARY } from './nav-items'

// Both presentations of the navigation read the same list; see nav-items.ts.
const navItems = SIDEBAR_PRIMARY.flatMap((to) => {
  const item = NAV_ITEMS.find((entry) => entry.to === to)
  return item ? [item] : []
}).filter((item) => !item.adminOnly)

const bottomItems = NAV_ITEMS.filter(
  (item) => item.to === '/profile' || item.to === '/settings'
)
const adminItems = NAV_ITEMS.filter((item) => item.adminOnly)

export function Sidebar() {
  // Identity comes from the shared auth context — a Clerk account in clerk
  // mode, the local session user otherwise.
  const { user, isAdmin } = useAuth()
  const displayName =
    user?.displayName || user?.username || user?.email || 'Account'

  return (
    <aside className="sticky top-[--sf-nav-h] self-start hidden h-[calc(100vh-var(--sf-nav-h))] w-[220px] flex-shrink-0 flex-col border-r border-sf-line lg:flex">
      <nav className="flex-1 overflow-y-auto px-3 pt-6">
        <div className="flex flex-col gap-1">
          {navItems.map((item) => (
            <SidebarLink key={item.to} {...item} />
          ))}
        </div>
      </nav>
      <div className="flex-shrink-0 border-t border-sf-line px-3 py-4">
        <div className="flex flex-col gap-1">
          {user && (
            <Link
              to="/profile"
              className="flex items-center gap-3 rounded-sf-sm px-3 py-2.5 transition-colors hover:bg-sf-panel"
            >
              {user.imageUrl ? (
                <img
                  src={user.imageUrl}
                  alt=""
                  className="size-8 flex-shrink-0 rounded-full object-cover ring-1 ring-sf-line"
                />
              ) : (
                <span className="grid size-8 flex-shrink-0 place-items-center rounded-full bg-sf-accent/20 text-sm font-bold text-sf-ink">
                  {displayName.charAt(0).toUpperCase()}
                </span>
              )}
              <span className="min-w-0">
                <span className="block truncate text-sm font-semibold text-sf-ink">
                  {displayName}
                </span>
                <span className="block truncate text-xs text-sf-ink-muted">
                  @{user.username || 'profile'}
                </span>
              </span>
            </Link>
          )}
          {bottomItems.map((item) => (
            <SidebarLink key={item.to} {...item} />
          ))}
          {isAdmin &&
            adminItems.map((item) => <SidebarLink key={item.to} {...item} />)}
        </div>
      </div>
    </aside>
  )
}

function SidebarLink({
  to,
  icon: Icon,
  label,
}: {
  to: string
  icon: React.ComponentType<{ className?: string }>
  label: string
}) {
  return (
    <Link
      to={to}
      className={cn(
        'flex items-center gap-3 rounded-sf-sm px-3 py-2.5 text-sm font-medium text-sf-ink-muted transition-colors hover:bg-sf-panel hover:text-sf-ink',
        '[&.active]:bg-sf-panel [&.active]:text-sf-ink'
      )}
    >
      <Icon className="h-5 w-5" />
      {label}
    </Link>
  )
}
