import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Compass } from 'lucide-react'
import { useApi } from '@/api/client'
import {
  useActivityFeed,
  usePlaybackEpisodes,
  usePlaybackMovies,
  usePopularMovies,
  usePopularShows,
  useRecommendationsMovies,
  useRecommendationsShows,
  useSocialRecommendationsMovies,
  useSocialRecommendationsShows,
  useTrendingMovies,
  useTrendingShows,
} from '@/api/hooks'
import { useTrendingLists } from '@/api/hooks/useLists'
import type {
  Movie,
  PlaybackItem,
  RecommendationItem,
  Show,
  TraktList,
  TrendingItem as TrendingMedia,
} from '@/api/types'
import { Page, ScrollRow, Section } from '@/components/layout'
import { ActivityItem, Button, PosterCard, SectionHead } from '@/components/ui'
import { listRowID } from '@/lib/ids'
import { firstImage } from '@/lib/images'
import { mediaLink } from '@/lib/media-url'

export const Route = createFileRoute('/')({
  component: HomePage,
})

/** One card on the home page, whatever endpoint it came from. */
interface HomeCard {
  key: string
  title: string
  meta?: string
  image?: string
  rating?: string
  type: 'movie' | 'series'
  to?: { to: string; id: string }
}

function HomePage() {
  const navigate = useNavigate()
  const api = useApi()

  const { data: playbackMovies } = usePlaybackMovies()
  const { data: playbackEpisodes } = usePlaybackEpisodes()
  const { data: recsMovies } = useRecommendationsMovies()
  const { data: recsShows } = useRecommendationsShows()
  const { data: socialMovies } = useSocialRecommendationsMovies()
  const { data: socialShows } = useSocialRecommendationsShows()
  const { data: trendingMovies } = useTrendingMovies()
  const { data: trendingShows } = useTrendingShows()
  const { data: popularMovies } = usePopularMovies()
  const { data: popularShows } = usePopularShows()
  const { data: trendingLists } = useTrendingLists(api)
  const { data: activity } = useActivityFeed({ limit: 20 })

  const continueWatching: PlaybackItem[] = [
    ...(playbackMovies ?? []),
    ...(playbackEpisodes ?? []),
  ]
    .filter((item) => item.progress > 0)
    .slice(0, 12)

  const personalized = toCards([
    ...(recsMovies ?? []),
    ...(recsShows ?? []),
  ]).slice(0, 12)

  const friends = toCards([
    ...(socialMovies ?? []),
    ...(socialShows ?? []),
  ]).slice(0, 12)

  const whatsShowing = dedupe([
    ...(trendingMovies ?? []).map(trendingAsMovie),
    ...(trendingShows ?? []).map(trendingAsShow),
  ]).slice(0, 18)

  const popular = dedupe([
    ...(popularMovies ?? []).map(movieCard),
    ...(popularShows ?? []).map(showCard),
  ])
    .filter((card) => !whatsShowing.some((shown) => shown.key === card.key))
    .slice(0, 18)

  const lists: HomeCard[] = (trendingLists ?? [])
    .map(({ list }) => listCard(list))
    .slice(0, 12)

  const openCard = (card: HomeCard) => {
    if (card.to) navigate({ to: card.to.to, params: { id: card.to.id } })
  }

  // Every row that can render counts: an account with playback progress but an
  // empty catalogue still has something on the page, and telling it the
  // catalogue is empty next to "Continue Watching" contradicts itself.
  const rows =
    continueWatching.length +
    personalized.length +
    friends.length +
    whatsShowing.length +
    popular.length +
    lists.length +
    (activity ?? []).length

  return (
    <Page>
      {continueWatching.length > 0 && (
        <Section>
          <SectionHead title="Continue Watching" />
          <ScrollRow>
            {continueWatching.map((item) => (
              <PosterCard
                key={`${item.type}-${item.id}-${item.episode?.season ?? ''}-${item.episode?.number ?? ''}`}
                title={
                  item.type === 'episode'
                    ? (item.show?.title ?? item.episode?.title ?? 'Unknown')
                    : (item.movie?.title ?? 'Unknown')
                }
                meta={
                  item.type === 'episode'
                    ? `S${item.episode?.season ?? '?'}E${item.episode?.number ?? '?'} — ${item.episode?.title ?? ''}`
                    : String(item.movie?.year ?? '')
                }
                progress={item.progress}
                aspect="2:3"
              />
            ))}
          </ScrollRow>
        </Section>
      )}

      {/* Recommendations lead the page, but they are not what the page depends
          on: a fresh account has none, and every row under this one still has
          something to show. */}
      <CardRow
        title="Recommended for You"
        cards={personalized}
        onOpen={openCard}
      />
      <CardRow title="Friends Are Watching" cards={friends} onOpen={openCard} />
      <CardRow
        title="What's showing"
        cards={whatsShowing}
        onOpen={openCard}
        eyebrow="Popular right now"
      />
      <CardRow
        title="Popular on this instance"
        cards={popular}
        onOpen={openCard}
        eyebrow="From this catalogue"
      />
      <CardRow
        title="Lists on this instance"
        cards={lists}
        onOpen={(card) => {
          if (card.to) navigate({ to: card.to.to, params: { id: card.to.id } })
        }}
        eyebrow="Shared by everyone here"
      />

      {(activity ?? []).length > 0 && (
        <Section>
          <SectionHead title="Activity" />
          <div className="space-y-2">
            {(activity ?? []).map((item) => (
              <ActivityItem key={item.id} item={item} />
            ))}
          </div>
        </Section>
      )}

      {rows === 0 && (
        <Section>
          <div className="glass flex flex-col items-center gap-4 rounded-[var(--sf-radius-lg)] px-6 py-16 text-center">
            <span className="grid size-14 place-items-center rounded-full bg-sf-accent/12 text-sf-accent-2">
              <Compass className="size-6" />
            </span>
            <h2 className="font-display text-xl font-bold text-sf-ink">
              Nothing here yet
            </h2>
            <p className="max-w-sm text-sm text-sf-ink-muted">
              This catalogue is empty. Search for a title to pull it in, then
              track what you watch.
            </p>
            <Button size="sm" onClick={() => navigate({ to: '/search' })}>
              Find something to watch
            </Button>
          </div>
        </Section>
      )}
    </Page>
  )
}

/** CardRow renders a titled horizontal row, or nothing when it is empty. */
function CardRow({
  title,
  cards,
  onOpen,
  eyebrow,
}: {
  title: string
  cards: HomeCard[]
  onOpen: (card: HomeCard) => void
  eyebrow?: string
}) {
  if (cards.length === 0) return null

  return (
    <Section>
      <SectionHead title={title} eyebrow={eyebrow} />
      <ScrollRow>
        {cards.map((card) => (
          <PosterCard
            key={card.key}
            title={card.title}
            meta={card.meta}
            image={card.image}
            rating={card.rating}
            type={card.type}
            aspect="2:3"
            onClick={() => onOpen(card)}
          />
        ))}
      </ScrollRow>
    </Section>
  )
}

// ─── Card builders ─────────────────────────────────────────────────────

function movieCard(movie: Movie): HomeCard {
  return {
    key: `movie-${cardKey(movie.ids)}`,
    title: movie.title,
    meta: movie.year ? String(movie.year) : undefined,
    image: firstImage(movie.images, 'poster'),
    rating: movie.rating ? (movie.rating / 2).toFixed(1) : undefined,
    type: 'movie',
    to: mediaLink('movie', movie.ids),
  }
}

function showCard(show: Show): HomeCard {
  return {
    key: `show-${cardKey(show.ids)}`,
    title: show.title,
    meta: show.year ? String(show.year) : undefined,
    image: firstImage(show.images, 'poster'),
    rating: show.rating ? (show.rating / 2).toFixed(1) : undefined,
    type: 'series',
    to: mediaLink('show', show.ids),
  }
}

/**
 * trendingAsMovie / trendingAsShow read a trending hit.
 *
 * The trending routes answer with the media rows themselves, while the
 * TrendingItem type describes a {watchers, movie} wrapper some providers return;
 * both shapes are accepted. The KIND comes from the endpoint, not from the
 * payload: at the minimal extended level a show carries no field a movie lacks,
 * so guessing from the body typed every show as a movie — which also collided
 * with a movie's card key and made the row drop one of them.
 */
function trendingAsMovie(item: TrendingMedia | Movie): HomeCard {
  const wrapped = item as TrendingMedia
  return movieCard(wrapped.movie ?? (item as Movie))
}

function trendingAsShow(item: TrendingMedia | Show): HomeCard {
  const wrapped = item as TrendingMedia
  return showCard(wrapped.show ?? (item as Show))
}

function recommendationCard(item: RecommendationItem): HomeCard {
  // A recommendation carries the provider id, not a local one: link by TMDB so
  // the detail route resolves it, and fall back to the local row id when the
  // provider id is missing.
  const ids = {
    trakt: item.tmdb_id ? 0 : item.id,
    slug: '',
    tmdb: item.tmdb_id || undefined,
  }
  return {
    key: `${item.media_type}-${item.id}`,
    title: item.title,
    meta: item.released?.slice(0, 4) || item.first_aired?.slice(0, 4),
    image: item.poster || undefined,
    type: item.media_type === 'movie' ? 'movie' : 'series',
    to: mediaLink(item.media_type === 'movie' ? 'movie' : 'show', ids),
  }
}

function listCard(list: TraktList): HomeCard {
  return {
    key: `list-${listRowID(list) || list.ids?.slug || list.name}`,
    title: list.name,
    meta: `${list.item_count ?? 0} items`,
    type: 'movie',
    to:
      listRowID(list) > 0
        ? { to: '/lists/$id', id: String(listRowID(list)) }
        : undefined,
  }
}

function toCards(items: RecommendationItem[]): HomeCard[] {
  return items.map(recommendationCard)
}

/**
 * cardKey is the identity a card is deduped by: the local row id, else the
 * provider id, else the slug. Without the provider id the trending and popular
 * rows produced identical keys for different titles, or no key at all, and the
 * same title appeared twice in one page.
 */
function cardKey(ids: Movie['ids'] | undefined): string {
  if (!ids) return 'unknown'
  return String(ids.trakt || ids.id || ids.tmdb || ids.slug || 'unknown')
}

function dedupe(cards: HomeCard[]): HomeCard[] {
  const seen = new Set<string>()
  return cards.filter((card) => {
    if (seen.has(card.key)) return false
    seen.add(card.key)
    return true
  })
}
