import { Link } from '@tanstack/react-router'
import { MoreHorizontal } from 'lucide-react'
import { useAuth } from '@/auth/context'
import { Dropdown } from '@/components/ui'
import { cn } from '@/lib/utils'
import { MOBILE_BAR, MOBILE_MORE, mobileBarLabel } from './nav-items'

/**
 * The phone / tablet navigation bar.
 *
 * Four destinations that fit a 390px bar plus one overflow control. The
 * overflow list is the same data the desktop sidebar renders (nav-items.ts), so
 * a destination cannot exist on one and be missing from the other — which is
 * how Lists became unreachable on a phone while the sidebar had it.
 */
export function MobileNav() {
  const { isAdmin } = useAuth()

  // `to` renders each overflow destination as a link; without it the rows are
  // buttons and the destinations cannot be opened in a new tab.
  const moreItems = MOBILE_MORE.filter(
    (item) => !item.adminOnly || isAdmin
  ).map((item) => ({
    label: item.label,
    icon: <item.icon className="size-4" />,
    to: item.to,
  }))

  return (
    <nav className="glass fixed bottom-0 left-0 right-0 z-50 border-t border-sf-line lg:hidden">
      <div className="mx-auto flex max-w-lg items-center justify-around py-2">
        {MOBILE_BAR.map((item) => (
          <Link
            key={item.to}
            to={item.to}
            className={cn(
              'flex flex-col items-center gap-0.5 rounded-sf-sm px-3 py-1 text-[11px] text-sf-ink-hint transition-colors',
              '[&.active]:text-sf-accent'
            )}
          >
            <item.icon className="h-5 w-5" />
            {mobileBarLabel(item)}
          </Link>
        ))}

        <Dropdown
          align="end"
          items={moreItems}
          trigger={
            <button
              type="button"
              className="flex cursor-pointer flex-col items-center gap-0.5 rounded-sf-sm px-3 py-1 text-[11px] text-sf-ink-hint transition-colors hover:text-sf-ink"
            >
              <MoreHorizontal className="h-5 w-5" />
              More
            </button>
          }
        />
      </div>
    </nav>
  )
}
