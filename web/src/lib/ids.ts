import type { Ids } from '@/api/types'

/**
 * localRowID is this instance's row id for a media object.
 *
 * The API serves the row id in the contract's `trakt` field — a client that
 * only knows the contract shape can address the row by it — and also in the
 * shorter `id` key that older responses used. Every local route (an episode
 * page, a person page, a comment target) is addressed by that number, so one
 * helper reads both spellings instead of each call site guessing.
 */
export function localRowID(ids: Ids | null | undefined): number {
  if (!ids) return 0
  return ids.trakt || ids.id || 0
}

/** tmdbID is the provider id a media object links by, or 0 when it has none. */
export function tmdbID(ids: Ids | null | undefined): number {
  return ids?.tmdb ?? 0
}

/** The bits of a list row that address it: the contract ids, or a bare row id. */
export type ListRowKey = {
  ids?: { trakt?: number; slug?: string } | null
  id?: number
}

/**
 * listRowID is the number that addresses a list on this instance.
 *
 * It lives in `ids.trakt` (the contract key), and this helper also accepts a
 * bare top-level `id` — an endpoint that answered the stored row instead of the
 * contract object left `ids` undefined, and reading `list.ids.trakt`
 * unconditionally is what threw and blanked the whole My Lists page. One helper
 * keeps a shape regression to a missing link rather than a dead page.
 */
export function listRowID(list: ListRowKey | null | undefined): number {
  if (!list) return 0
  return list.ids?.trakt || list.id || 0
}
