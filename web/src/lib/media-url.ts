import type { Ids } from '@/api/types'

/**
 * A media URL carries the TMDB id, not the local row id.
 *
 * The row id is an implementation detail: it comes from our own sequence, it
 * changes when a catalogue is rebuilt, and `/shows/3` names a different title on
 * every install — and on this one a different title from movie row 3. The TMDB
 * id is the identity the catalogue is ingested from, so `/shows/4194` says which
 * title it is, survives a database reset, and matches what a media server or
 * another install would call the same show.
 *
 * Two fallbacks exist for a row TMDB has no id for, and they are DIFFERENT
 * namespaces on purpose: a slug is its own id form, and `local-<row id>` says
 * "this number is our row id". A bare number is always a TMDB id, so an id can
 * never be read two ways — the alternative (retrying a failed TMDB lookup as a
 * row id) opens an unrelated title whenever the two numbers collide.
 */
export function mediaID(ids: Ids | null | undefined): string | undefined {
  if (!ids) return undefined
  if (typeof ids.tmdb === 'number' && ids.tmdb > 0) return String(ids.tmdb)
  if (ids.slug) return String(ids.slug)
  if (ids.trakt > 0) return localMediaID(ids.trakt)
  return undefined
}

/**
 * localMediaID spells a row id as the URL segment that means "row id".
 *
 * It exists for the links that only ever had a row id: a notification or a
 * social item whose payload carries `item.id`. Prefer mediaID — a canonical URL
 * should not depend on our sequence.
 */
export function localMediaID(rowID: string | number): string {
  return `local-${rowID}`
}

/** rowIDFromParam reads a `local-<n>` segment back to the row id, or null. */
export function rowIDFromParam(id: string | number | undefined): string | null {
  const match = /^local-(\d+)$/.exec(String(id ?? '').trim())
  return match ? match[1] : null
}

/** Media kinds with a detail route. Seasons and episodes are addressed by row id. */
export type MediaKind = 'movie' | 'show'

/** mediaLink is the router target for a media object, or undefined if it has no id. */
export function mediaLink(
  kind: MediaKind,
  ids: Ids | null | undefined
): { to: '/movies/$id' | '/shows/$id'; id: string } | undefined {
  const id = mediaID(ids)
  if (!id) return undefined
  return { to: kind === 'movie' ? '/movies/$id' : '/shows/$id', id }
}

/** isNumericID reports whether a route param is a bare number rather than a slug. */
export function isNumericID(id: string | number | undefined): boolean {
  return /^\d+$/.test(String(id ?? '').trim())
}
