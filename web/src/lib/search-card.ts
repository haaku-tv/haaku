import type { Ids, Person, SearchResult } from '@/api/types'
import { episodeStill, firstImage } from '@/lib/images'
import { mediaLink } from '@/lib/media-url'

/**
 * cardOf reduces a search result to what a card needs: which media type it is,
 * how to render it, and how to identify it at the provider when it is not in
 * the catalogue yet.
 */
export interface CardModel {
  mediaType: 'movie' | 'show' | 'episode' | 'person' | 'list'
  title: string
  meta?: string
  image?: string
  rating?: string
  aspect: '16:9' | '2:3'
  ids: Ids
  /** tmdbId is the external id /v1/media/resolve ingests from, when needed. */
  tmdbId?: number
  /** inCatalog means the row exists locally: link straight to it. */
  inCatalog: boolean
  navigateTo?: { to: string; id: string }
}

/** personMeta is the line under a person's name: what they are known for. */
function personMeta(person: Person): string | undefined {
  const knownFor = (person as Person & { known_for_department?: string })
    .known_for_department
  return knownFor || undefined
}

export function cardOf(result: SearchResult): CardModel | null {
  switch (result.type) {
    case 'movie': {
      const m = result.movie
      if (!m) return null
      // Only a NUMERIC local row id can be navigated to. A live TVDB hit has
      // no local id but does carry a slug, and treating the slug as an id
      // navigated to /movies/breaking-bad, a row that was never ingested.
      const localId = m.ids.trakt || m.ids.id || 0
      const inCatalog = m.in_catalog !== false && localId > 0
      return {
        mediaType: 'movie',
        title: String(m.title ?? ''),
        meta: m.year ? String(m.year) : undefined,
        image: firstImage(m.images, 'poster'),
        rating: m.rating ? (Number(m.rating) / 2).toFixed(1) : undefined,
        aspect: '2:3',
        ids: m.ids,
        tmdbId: m.ids.tmdb,
        inCatalog,
        navigateTo: inCatalog ? mediaLink('movie', m.ids) : undefined,
      }
    }
    case 'show': {
      const s = result.show
      if (!s) return null
      const localId = s.ids.trakt || s.ids.id || 0
      const inCatalog = s.in_catalog !== false && localId > 0
      return {
        mediaType: 'show',
        title: String(s.title ?? ''),
        meta: s.year ? String(s.year) : undefined,
        image: firstImage(s.images, 'poster'),
        rating: s.rating ? (Number(s.rating) / 2).toFixed(1) : undefined,
        aspect: '2:3',
        ids: s.ids,
        tmdbId: s.ids.tmdb,
        inCatalog,
        navigateTo: inCatalog ? mediaLink('show', s.ids) : undefined,
      }
    }
    case 'episode': {
      const ep = result.episode
      if (!ep) return null
      const localId = ep.ids.trakt || ep.ids.id || 0
      return {
        mediaType: 'episode',
        title: String(ep.title ?? ''),
        meta: [ep.show_title, `S${ep.season} E${ep.number}`]
          .filter(Boolean)
          .join(' · '),
        image: episodeStill(ep.images),
        aspect: '16:9',
        ids: ep.ids,
        inCatalog: localId > 0,
        navigateTo:
          localId > 0
            ? { to: '/episodes/$id', id: String(localId) }
            : undefined,
      }
    }
    case 'person': {
      const p = result.person
      if (!p) return null
      // A person is addressed by their local row id, like every other local
      // route. This used to read the slug, which the person payload does not
      // carry, so every result linked to /people/undefined.
      const localId = p.ids.trakt || p.ids.id || 0
      return {
        mediaType: 'person',
        title: String(p.name ?? ''),
        meta: personMeta(p),
        image: firstImage(p.images, 'headshot', 'fanart', 'poster'),
        aspect: '2:3',
        ids: p.ids,
        inCatalog: localId > 0,
        navigateTo:
          localId > 0 ? { to: '/people/$id', id: String(localId) } : undefined,
      }
    }
    case 'list': {
      const l = result.list
      if (!l) return null
      return {
        mediaType: 'list',
        title: String(l.name ?? ''),
        meta: `${String(l.item_count ?? 0)} items · by ${String(l.user?.username ?? '')}`,
        aspect: '16:9',
        ids: { trakt: 0, slug: '' },
        inCatalog: true,
      }
    }
    default:
      return null
  }
}
