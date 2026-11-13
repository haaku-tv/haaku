import { api } from '@/api/fetch'
import { isNumericID, rowIDFromParam } from '@/lib/media-url'

/**
 * getMedia reads a media resource by the id a media URL carries.
 *
 * The URL's id form decides which namespace is asked for, and it is asked for
 * ONCE — never retried under another interpretation, because a TMDB id and a row
 * id that collide would then open an unrelated title:
 *
 * - a bare number is the TMDB id → `?id_type=tmdb`. The API's bare-numeric form
 *   addresses the local row id, so this has to be explicit.
 * - `local-<n>` is our row id → the bare-numeric path. This is the form the
 *   links that only ever had a row id carry now (notifications, social items).
 * - anything else is a slug, which the resolver reads directly.
 *
 * A URL minted BEFORE this change is also a bare number, and there the two
 * meanings are indistinguishable: `/shows/3` was row 3 and now reads as TMDB 3.
 * It 404s — unless TMDB has an id 3 of that media type, in which case it opens
 * that title. That is the one shape this change cannot recover, and it is why
 * nothing is retried across namespaces: the app only ever mints a canonical or a
 * `local-` link, so only links from outside it can be affected.
 *
 * `rest` is the sub-resource path after the id (`/seasons/1/episodes`).
 * `beforeID` is the segment that precedes it, for the routes spelled
 * `/v1/shows/recommendations/{id}` rather than `/v1/shows/{id}/...`.
 */
export async function getMedia<T>(
  kind: 'movies' | 'shows',
  id: string | number | undefined,
  rest = '',
  beforeID = ''
): Promise<T> {
  const rowID = rowIDFromParam(id)
  const segment = rowID ?? String(id ?? '')
  const path = `/v1/${kind}/${beforeID}${encodeURIComponent(segment)}${rest}`
  if (rowID !== null) return api.get<T>(path)
  if (!isNumericID(id)) return api.get<T>(path)
  return api.get<T>(path, { id_type: 'tmdb' })
}
