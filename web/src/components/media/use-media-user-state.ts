import {
  useFavorites,
  useHistory,
  useRatings,
  useWatchlist,
} from '@/api/hooks/useSync'
import type { Ids } from '@/api/types'
import { localRowID } from '@/lib/ids'

export type MediaUserState = {
  watched: boolean
  watchlisted: boolean
  favorite: boolean
  /** 1–10, 0 when unrated. */
  rating: number
}

/**
 * Whether this account has already watched, watchlisted, favorited or rated a
 * title.
 *
 * The sync reads answer whole pages of the account's items, so membership is
 * derived from them here instead of from one request per title: the media page
 * needs the answer for exactly one row, but the answer lives in lists the app
 * caches anyway. A list that is still loading reports "not a member", which is
 * the safe default — an optimistic toggle wins over it once the user acts.
 */
export function useMediaUserState(
  kind: 'movie' | 'show',
  ids: Ids | undefined
): MediaUserState {
  const target = localRowID(ids)

  const { data: watchlist } = useWatchlist({ type: kind, limit: 250 })
  const { data: favorites } = useFavorites({ type: kind, limit: 250 })
  const { data: ratings } = useRatings({ type: kind, limit: 250 })
  const { data: history } = useHistory({ type: kind, limit: 250 })

  const matches = (entry: {
    movie?: { ids?: Ids }
    show?: { ids?: Ids }
  }): boolean => {
    const row = entry.movie ?? entry.show
    return localRowID(row?.ids) === target && target > 0
  }

  const ratingItem = (ratings?.data ?? []).find(matches)

  return {
    watched: (history?.data ?? []).some(matches),
    watchlisted: (watchlist?.data ?? []).some(matches),
    favorite: (favorites?.data ?? []).some(matches),
    rating: ratingItem?.rating ?? 0,
  }
}
