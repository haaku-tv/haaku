import { Bookmark, BookmarkCheck, Check, Heart, Play } from 'lucide-react'
import { useState } from 'react'
import {
  useAddRating,
  useAddToFavorites,
  useAddToHistory,
  useAddToWatchlist,
  useRemoveFromFavorites,
  useRemoveFromHistory,
  useRemoveFromWatchlist,
} from '@/api/hooks/useSync'
import type { Ids } from '@/api/types'
import { useAuth } from '@/auth/context'
import { Button } from '@/components/ui'
import { cn } from '@/lib/utils'
import { useMediaUserState } from './use-media-user-state'

export type MediaActionsProps = {
  kind: 'movie' | 'show'
  ids: Ids | undefined
  trailer?: string
  /** The title's own rating, shown on the primary action's tooltip. */
  className?: string
}

/**
 * The action bar: watch it, save it, love it, rate it.
 *
 * Watched, watchlisted and favorited all read from the account's own lists
 * (useMediaUserState) and toggle optimistically — the sync write is a POST that
 * answers with a summary, so waiting for it would leave the button wrong for
 * the length of a round trip on a box that may be a Raspberry Pi.
 */
export function MediaActions({
  kind,
  ids,
  trailer,
  className,
}: MediaActionsProps) {
  const { isSignedIn } = useAuth()
  const state = useMediaUserState(kind, ids)

  const addHistory = useAddToHistory()
  const removeHistory = useRemoveFromHistory()
  const addWatchlist = useAddToWatchlist()
  const removeWatchlist = useRemoveFromWatchlist()
  const addFavorite = useAddToFavorites()
  const removeFavorite = useRemoveFromFavorites()
  const addRating = useAddRating()

  const [watched, setWatched] = useState<boolean | null>(null)
  const [watchlisted, setWatchlisted] = useState<boolean | null>(null)
  const [favorite, setFavorite] = useState<boolean | null>(null)
  const [rating, setRating] = useState<number | null>(null)

  const isWatched = watched ?? state.watched
  const isWatchlisted = watchlisted ?? state.watchlisted
  const isFavorite = favorite ?? state.favorite
  const userRating = rating ?? state.rating

  if (!isSignedIn) {
    return (
      <Button size="sm" variant="glass" disabled>
        Sign in to track this
      </Button>
    )
  }

  if (!ids) return null

  const payload = {
    [kind === 'movie' ? 'movies' : 'shows']: [{ ids }],
  }

  return (
    <div className={cn('flex flex-wrap items-center gap-2', className)}>
      <Button
        size="default"
        variant={isWatched ? 'glass' : 'primary'}
        onClick={() => {
          const next = !isWatched
          setWatched(next)
          if (next) addHistory.mutate(payload)
          else removeHistory.mutate(payload)
        }}
      >
        {isWatched ? (
          <Check className="size-4" />
        ) : (
          <Play className="size-4 fill-current" />
        )}
        {isWatched ? 'Watched' : 'Mark watched'}
      </Button>

      <Button
        size="sm"
        variant={isWatchlisted ? 'primary' : 'glass'}
        onClick={() => {
          const next = !isWatchlisted
          setWatchlisted(next)
          if (next) addWatchlist.mutate(payload)
          else removeWatchlist.mutate(payload)
        }}
        title={isWatchlisted ? 'Remove from watchlist' : 'Add to watchlist'}
      >
        {isWatchlisted ? (
          <BookmarkCheck className="size-4" />
        ) : (
          <Bookmark className="size-4" />
        )}
        {isWatchlisted ? 'Watchlisted' : 'Watchlist'}
      </Button>

      <Button
        size="sm"
        variant={isFavorite ? 'primary' : 'glass'}
        onClick={() => {
          const next = !isFavorite
          setFavorite(next)
          if (next) addFavorite.mutate(payload)
          else removeFavorite.mutate(payload)
        }}
        title={isFavorite ? 'Remove from favorites' : 'Add to favorites'}
      >
        <Heart
          className={cn(
            'size-4',
            isFavorite && 'fill-current text-sf-accent-3'
          )}
        />
        {isFavorite ? 'Favorited' : 'Favorite'}
      </Button>

      <span className="ml-1 flex items-center gap-0.5">
        {Array.from({ length: 10 }, (_, index) => index + 1).map((score) => (
          <button
            key={score}
            type="button"
            onClick={() => {
              setRating(score)
              addRating.mutate({
                [kind === 'movie' ? 'movies' : 'shows']: [
                  { ids, rating: score },
                ],
              })
            }}
            aria-label={`Rate ${score} out of 10`}
            className="cursor-pointer p-0.5 transition-transform hover:scale-110"
          >
            <span
              className={cn(
                'block size-2 rounded-full transition-colors',
                score <= userRating ? 'bg-sf-accent-2' : 'bg-sf-ink-hint'
              )}
            />
          </button>
        ))}
        {userRating > 0 && (
          <span className="ml-1.5 text-xs font-bold text-sf-ink-muted tabular-nums">
            {userRating}/10
          </span>
        )}
      </span>

      {trailer && (
        <a
          href={trailer}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-2 rounded-full border border-sf-line bg-sf-panel px-3.5 py-1.5 text-xs font-semibold text-sf-ink-muted transition-colors hover:text-sf-ink"
        >
          <Play className="size-3.5" />
          Trailer
        </a>
      )}
    </div>
  )
}
