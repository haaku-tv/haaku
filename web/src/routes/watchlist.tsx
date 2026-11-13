import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Grid3X3, Search, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useRemoveFromWatchlist, useWatchlist } from '@/api/hooks'
import type { WatchlistItem } from '@/api/types'
import { useAuth } from '@/auth/context'
import { Page, PosterGrid, Section } from '@/components/layout'
import {
  Button,
  FilterPill,
  PaginationControls,
  PosterCard,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui'
import { firstImage } from '@/lib/images'
import { mediaLink } from '@/lib/media-url'

export const Route = createFileRoute('/watchlist')({
  component: WatchlistPage,
})

type MediaTypeFilter = 'all' | 'movies' | 'shows'

const typeMap: Record<MediaTypeFilter, string | undefined> = {
  all: undefined,
  movies: 'movie',
  shows: 'show',
}

/**
 * The sort values the API actually accepts.
 *
 * The handler switches on rank / listed / added / title and falls back to rank
 * for anything else, so a menu built from wishful names is a menu whose entries
 * silently do the same thing.
 */
const SORT_OPTIONS = [
  { value: 'rank', label: 'Rank' },
  { value: 'title', label: 'Title' },
  { value: 'added', label: 'Recently added' },
  { value: 'listed', label: 'Recently listed' },
] as const

function WatchlistPage() {
  const { isSignedIn } = useAuth()
  const [mediaType, setMediaType] = useState<MediaTypeFilter>('all')
  const [sortBy, setSortBy] = useState<string>('rank')
  const [page, setPage] = useState(1)

  const { data, isLoading, isError } = useWatchlist({
    type: typeMap[mediaType],
    sortBy,
    page,
    limit: 24,
  })

  const removeMutation = useRemoveFromWatchlist()

  const items = data?.data ?? []
  const totalPages = data?.total_pages ?? 1

  const handleRemove = (item: WatchlistItem) => {
    removeMutation.mutate(buildRemoveBody(item))
  }

  return (
    <Page>
      <Section>
        <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="font-display text-2xl font-bold text-sf-ink">
              Watchlist
            </h1>
            {data && (
              <p className="mt-1 text-sm text-sf-ink-muted">
                {data.total} {data.total === 1 ? 'item' : 'items'}
              </p>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <div className="flex gap-1">
              {(
                [
                  ['all', 'All'],
                  ['movies', 'Movies'],
                  ['shows', 'Shows'],
                ] as const
              ).map(([value, label]) => (
                <FilterPill
                  key={value}
                  active={mediaType === value}
                  onClick={() => {
                    setMediaType(value)
                    setPage(1)
                  }}
                >
                  {label}
                </FilterPill>
              ))}
            </div>

            <Select
              value={sortBy}
              onValueChange={(value) => {
                setSortBy(value)
                setPage(1)
              }}
            >
              <SelectTrigger className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SORT_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        {isLoading ? (
          <PosterGrid>
            {Array.from({ length: 12 }, (_, index) => (
              <div
                key={index}
                className="glass aspect-[2/3] animate-pulse rounded-sf"
              />
            ))}
          </PosterGrid>
        ) : isError ? (
          <EmptyState
            title="Could not load your watchlist"
            body="The request failed. Try again in a moment."
          />
        ) : items.length === 0 ? (
          <EmptyState
            title="Your watchlist is empty"
            body={
              mediaType === 'all'
                ? 'Browse trending shows and movies to find something to watch.'
                : `No ${mediaType} on your watchlist yet.`
            }
          />
        ) : (
          <>
            <PosterGrid>
              {items.map((item) => (
                <WatchlistCard
                  key={item.id}
                  item={item}
                  onRemove={() => handleRemove(item)}
                />
              ))}
            </PosterGrid>

            {totalPages > 1 && (
              <div className="mt-8 flex justify-center">
                <PaginationControls
                  currentPage={page}
                  totalPages={totalPages}
                  onPageChange={setPage}
                />
              </div>
            )}
          </>
        )}

        {!isSignedIn && (
          <p className="mt-6 text-sm text-sf-ink-muted">
            Sign in to keep a watchlist.
          </p>
        )}
      </Section>
    </Page>
  )
}

/**
 * One watchlisted title.
 *
 * The card carries the poster the payload already holds and opens the title's
 * page; the remove control stops the click it sits on top of so removing a row
 * does not also navigate to it. Episodes are listed with their show's artwork
 * because a watchlist row for an episode carries no images of its own.
 */
function WatchlistCard({
  item,
  onRemove,
}: {
  item: WatchlistItem
  onRemove: () => void
}) {
  const media = item.movie ?? item.show
  if (!media) return null

  const kind = item.movie ? 'movie' : 'show'
  const link = mediaLink(kind, media.ids)
  const poster = firstImage(media.images, 'poster', 'thumb')
  const year = media.year ? String(media.year) : undefined
  const rating = media.rating ? media.rating.toFixed(1) : undefined

  const card = (
    <PosterCard
      title={media.title}
      meta={year}
      image={poster}
      rating={rating}
      aspect="2:3"
      type={item.movie ? 'movie' : 'series'}
    />
  )

  return (
    <div className="group relative">
      {/* A real link, not a clickable div: the remove control is a sibling so
          the two targets never nest, and Enter/Space work without a handler. */}
      {link ? (
        <Link to={link.to} params={{ id: link.id }} className="block">
          {card}
        </Link>
      ) : (
        card
      )}
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation()
          onRemove()
        }}
        className="absolute top-2 right-2 rounded-full bg-black/70 p-1.5 text-white opacity-0 backdrop-blur-sm transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
        aria-label={`Remove ${media.title} from watchlist`}
      >
        <Trash2 className="size-3.5" />
      </button>
    </div>
  )
}

function EmptyState({ title, body }: { title: string; body: string }) {
  const navigate = useNavigate()
  return (
    <div className="glass flex flex-col items-center gap-4 rounded-[var(--sf-radius-lg)] px-6 py-14 text-center">
      <span className="grid size-14 place-items-center rounded-full bg-sf-accent/12 text-sf-accent-2">
        <Grid3X3 className="size-6" />
      </span>
      <h3 className="font-display text-xl font-bold text-sf-ink">{title}</h3>
      <p className="max-w-sm text-sm text-sf-ink-muted">{body}</p>
      <Button
        size="sm"
        variant="glass"
        onClick={() => navigate({ to: '/search' })}
      >
        <Search className="size-4" />
        Find something
      </Button>
    </div>
  )
}

function buildRemoveBody(item: WatchlistItem) {
  if (item.movie) {
    return { movies: [{ ids: item.movie.ids }] }
  }
  if (item.show) {
    return { shows: [{ ids: item.show.ids }] }
  }
  if (item.episode) {
    return { episodes: [{ ids: item.episode.ids }] }
  }
  return { movies: [] }
}
