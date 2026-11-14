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
