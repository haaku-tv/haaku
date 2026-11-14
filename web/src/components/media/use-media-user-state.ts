import { useQueries } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useApi } from '@/api/client'
import {
  useFavorites,
  useHistory,
  useRatings,
  useWatchlist,
} from '@/api/hooks/useSync'
import { parsePage } from '@/api/pagination'
import type { Ids } from '@/api/types'
import { localRowID } from '@/lib/ids'

export type MediaUserState = {
  watched: boolean
  watchlisted: boolean
  favorite: boolean
  /** 1–10, 0 when unrated. */
  rating: number
}

/** The sync reads that answer "is this title in that list?". */
type SyncList = 'watchlist' | 'favorites' | 'ratings' | 'history'

/**
 * A page of 250 is not the whole account. The rest of the list is read too, but
 * the walk stops somewhere: a membership check is not worth an unbounded scan
 * of a list with tens of thousands of rows, and no personal account reaches
 * this cap.
 */
const PAGE_SIZE = 250
const MAX_PAGES = 20

type SyncEndpoints = {
  watchlist: '/v1/sync/watchlist'
  favorites: '/v1/sync/favorites'
  ratings: '/v1/sync/ratings'
  history: '/v1/sync/history'
}

const ENDPOINTS: SyncEndpoints = {
  watchlist: '/v1/sync/watchlist',
  favorites: '/v1/sync/favorites',
  ratings: '/v1/sync/ratings',
  history: '/v1/sync/history',
}

/** What the hooks take: numbers, which they serialise themselves. */
function listParams(kind: 'movie' | 'show', page: number) {
  return { type: kind, page, limit: PAGE_SIZE }
}

/** What the typed client takes: the same values as query strings. */
function queryParams(kind: 'movie' | 'show', page: number) {
  return { type: kind, page: String(page), limit: String(PAGE_SIZE) }
}

/**
 * Whether this account has already watched, watchlisted, favorited or rated a
 * title.
 *
 * The sync reads are paged, and treating page one as the whole list is how the
 * action bar ends up offering "Mark watched" for something already watched: a
 * title past the first page reads as absent, and the next click sends an add
 * where the account wanted a removal.
 *
 * The first page comes from the same hooks the rest of the app uses, so the
 * detail page paints straight from cache; only the follow-up pages are fetched
 * here, under the same query-key prefix, so a mutation's invalidation refreshes
 * them as well.
 */
export function useMediaUserState(
  kind: 'movie' | 'show',
  ids: Ids | undefined
): MediaUserState {
  const target = localRowID(ids)
  const api = useApi()

  const { data: watchlist } = useWatchlist(listParams(kind, 1))
  const { data: favorites } = useFavorites(listParams(kind, 1))
  const { data: ratings } = useRatings(listParams(kind, 1))
  const { data: history } = useHistory(listParams(kind, 1))

  const firstPages = useMemo(
    () => ({ watchlist, favorites, ratings, history }),
    [watchlist, favorites, ratings, history]
  )

  // The follow-up pages each list needs, derived from its first page's total.
  const extraPages = useMemo(() => {
    const wanted: { list: SyncList; page: number }[] = []
    for (const list of Object.keys(ENDPOINTS) as SyncList[]) {
      const first = firstPages[list]
      const totalPages = Math.min(
        Math.ceil((first?.total ?? 0) / PAGE_SIZE),
        MAX_PAGES
      )
      for (let page = 2; page <= totalPages; page++) {
        wanted.push({ list, page })
      }
    }
    return wanted
  }, [firstPages])

  const extras = useQueries({
    queries: extraPages.map(({ list, page }) => ({
      queryKey: [list, listParams(kind, page)],
      queryFn: async () => {
        if (!api) return []
        const params = { query: queryParams(kind, page) as never }
        const response = await api.GET(ENDPOINTS[list], { params } as never)
        return parsePage<unknown>(response, PAGE_SIZE).data
      },
      staleTime: 60_000,
    })),
  })

  const rowsOf = (list: SyncList): unknown[] => {
    const rows: unknown[] = [...(firstPages[list]?.data ?? [])]
    extraPages.forEach((extra, index) => {
      if (extra.list !== list) return
      const data = extras[index]?.data
      if (data) rows.push(...(data as unknown[]))
    })
    return rows
  }

  const matches = (entry: unknown): boolean => {
    const row = entry as { movie?: { ids?: Ids }; show?: { ids?: Ids } }
    const media = row.movie ?? row.show
    return localRowID(media?.ids) === target && target > 0
  }

  const ratingItem = rowsOf('ratings').filter(matches).pop() as
    | { rating?: number }
    | undefined

  return {
    watched: rowsOf('history').some(matches),
    watchlisted: rowsOf('watchlist').some(matches),
    favorite: rowsOf('favorites').some(matches),
    rating: ratingItem?.rating ?? 0,
  }
}
