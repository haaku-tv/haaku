import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Calendar, Clock, List, Users } from 'lucide-react'
import {
  useMovie,
  useMoviePeople,
  useMovieRecommendations,
  useMovieStats,
} from '@/api/hooks/useMovies'
import { Page, ScrollRow, Section } from '@/components/layout'
import {
  CastRow,
  CrewLine,
  DetailHero,
  MediaActions,
  MetadataBadges,
  ReviewsSection,
  ScoreRow,
} from '@/components/media'
import {
  Button,
  InfoPanel,
  PosterCard,
  SectionHead,
  ShimmerText,
  StatCard,
} from '@/components/ui'
import { localRowID, tmdbID } from '@/lib/ids'
import { firstImage } from '@/lib/images'
import { mediaLink } from '@/lib/media-url'
import { formatDate } from '@/lib/utils'

export const Route = createFileRoute('/movies/$id')({
  component: MovieDetailPage,
})

function MovieDetailPage() {
  const { id } = Route.useParams()
  const navigate = useNavigate()

  const { data: movie, isLoading, isError } = useMovie(id)
  const { data: people } = useMoviePeople(id)
  const { data: stats } = useMovieStats(id)
  const { data: recommendations } = useMovieRecommendations(id)

  if (isLoading) {
    return (
      <Page>
        <div className="space-y-6 py-16">
          <ShimmerText className="h-[420px] w-full rounded-sf-xl" />
          <ShimmerText className="h-8 w-64" />
          <ShimmerText className="h-4 w-full" />
        </div>
      </Page>
    )
  }

  if (isError || !movie) {
    return (
      <Page>
        <div className="flex flex-col items-center gap-4 py-24 text-center">
          <p className="text-lg font-semibold text-sf-ink">Movie not found</p>
          <p className="max-w-sm text-sm text-sf-ink-muted">
            It may not be in this catalogue yet. Search for it to add it.
          </p>
          <Button variant="glass" onClick={() => navigate({ to: '/search' })}>
            Search movies
          </Button>
        </div>
      </Page>
    )
  }

  const cast = people?.cast ?? []
  const crew = people?.crew ?? []
  const rowID = localRowID(movie.ids)
  const backdrop = firstImage(movie.images, 'fanart', 'clearart')
  const poster = firstImage(movie.images, 'poster', 'thumb')

  return (
    <div className="pb-16">
      <DetailHero
        context={
          <>
            <span>Movie</span>
            {movie.status && (
              <>
                <span aria-hidden="true">·</span>
                <span>{movie.status}</span>
              </>
            )}
          </>
        }
        title={movie.title}
        tagline={movie.tagline}
        backdrop={backdrop}
        poster={poster ?? ''}
        posterFallback={movie.title}
        meta={
          <MetadataBadges
            year={movie.year}
            certification={movie.certification}
            runtime={movie.runtime}
          />
        }
        score={<ScoreRow rating={movie.rating} votes={movie.votes} />}
        overview={movie.overview}
        crewLine={<CrewLine crew={crew} genres={movie.genres} />}
        actions={
          <MediaActions
            key={rowID}
            kind="movie"
            ids={movie.ids}
            trailer={movie.trailer}
          />
        }
      />

      <Page className="pt-8">
        <Section>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            {stats?.watchers !== undefined && (
              <StatCard label="Watchers" value={stats.watchers} icon={Users} />
            )}
            {stats?.plays !== undefined && (
              <StatCard label="Plays" value={stats.plays} icon={Clock} />
            )}
            {stats?.lists !== undefined && (
              <StatCard label="Lists" value={stats.lists} icon={List} />
            )}
            {movie.released && (
              <StatCard
                label="Released"
                value={formatDate(movie.released)}
                icon={Calendar}
              />
            )}
          </div>
        </Section>

        {cast.length > 0 && (
          <Section>
            <SectionHead title="Cast" />
            <CastRow cast={cast} />
          </Section>
        )}

        <Section>
          <SectionHead title="Details" />
          <InfoPanel>
            <div className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-2">
              <Fact label="Original title" value={movie.title} />
              {movie.released && (
                <Fact label="Released" value={formatDate(movie.released)} />
              )}
              {movie.runtime ? (
                <Fact label="Runtime" value={`${movie.runtime} min`} />
              ) : null}
              {movie.certification && (
                <Fact label="Certification" value={movie.certification} />
              )}
              {movie.language && (
                <Fact label="Language" value={movie.language} />
              )}
              {movie.status && <Fact label="Status" value={movie.status} />}
              {movie.ids.tmdb && (
                <Fact label="TMDB" value={`#${tmdbID(movie.ids)}`} />
              )}
            </div>
          </InfoPanel>
        </Section>

        <ReviewsSection kind="movie" rowID={rowID} />

        {recommendations && recommendations.length > 0 && (
          <Section>
            <SectionHead title="More like this" />
            <ScrollRow>
              {recommendations.map((item) => (
                <PosterCard
                  key={item.ids?.trakt ?? item.ids?.slug}
                  title={item.title}
                  meta={item.year ? String(item.year) : undefined}
                  image={firstImage(item.images, 'poster')}
                  rating={item.rating ? item.rating.toFixed(1) : undefined}
                  type="movie"
                  aspect="2:3"
                  onClick={() => {
                    const link = mediaLink('movie', item.ids)
                    if (link) navigate({ to: link.to, params: { id: link.id } })
                  }}
                />
              ))}
            </ScrollRow>
          </Section>
        )}
      </Page>
    </div>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-sf-line/60 py-1.5 text-sm">
      <span className="text-sf-ink-faint">{label}</span>
      <span className="text-right font-medium text-sf-ink-muted">{value}</span>
    </div>
  )
}
