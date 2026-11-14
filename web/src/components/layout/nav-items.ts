import {
  Bookmark,
  Calendar,
  Clock,
  Home,
  List,
  Search,
  Settings,
  Shield,
  User,
  Users,
} from 'lucide-react'
import type { ComponentType } from 'react'

export type NavItem = {
  to: string
  icon: ComponentType<{ className?: string }>
  label: string
  /** Short label for the phone bar, where four words plus an icon must fit. */
  shortLabel?: string
  /**
   * Where the destination lives on a phone: on the bar itself, inside the bar's
   * overflow sheet, or nowhere (it is reachable from another page).
   *
   * The sidebar and the phone bar used to keep separate lists, which is how the
   * phone lost Lists, Calendar and Social for months while the sidebar had them.
   */
  mobile: 'bar' | 'more' | 'hidden'
  /** Admin-only entries are dropped for everyone else at render time. */
  adminOnly?: boolean
}

/**
 * Every destination the app shell can offer. One list, two presentations: the
 * desktop sidebar shows the primary and secondary groups, the phone bar shows
 * what fits on the bar and puts the rest behind its overflow control.
 */
export const NAV_ITEMS: NavItem[] = [
  { to: '/', icon: Home, label: 'Home', mobile: 'bar' },
  { to: '/search', icon: Search, label: 'Search', mobile: 'bar' },
  {
    to: '/watchlist',
    icon: Bookmark,
    label: 'Watchlist',
    shortLabel: 'Watch',
    mobile: 'bar',
  },
  {
    to: '/lists',
    icon: List,
    label: 'Lists',
    shortLabel: 'Lists',
    mobile: 'bar',
  },
  {
    to: '/history',
    icon: Clock,
    label: 'History',
    shortLabel: 'History',
    mobile: 'more',
  },
  { to: '/calendar', icon: Calendar, label: 'Calendar', mobile: 'more' },
  { to: '/social', icon: Users, label: 'Social', mobile: 'more' },
  {
    to: '/profile',
    icon: User,
    label: 'Profile',
    shortLabel: 'Profile',
    mobile: 'more',
  },
  { to: '/settings', icon: Settings, label: 'Settings', mobile: 'more' },
  {
    to: '/admin',
    icon: Shield,
    label: 'Admin',
    mobile: 'more',
    adminOnly: true,
  },
]

/** The entries the sidebar lists above its account block. */
export const SIDEBAR_PRIMARY = [
  '/',
  '/search',
  '/watchlist',
  '/history',
  '/lists',
  '/calendar',
  '/social',
]

/** The entries the phone bar shows inline, in order. */
export const MOBILE_BAR = NAV_ITEMS.filter((item) => item.mobile === 'bar')

/** The entries the phone bar's overflow control lists. */
export const MOBILE_MORE = NAV_ITEMS.filter((item) => item.mobile === 'more')

/** mobileBarLabel is the label a phone bar uses for an entry. */
export function mobileBarLabel(item: NavItem): string {
  return item.shortLabel ?? item.label
}
