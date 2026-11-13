import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Calendar, Clock, List, Users } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import {
  useShow,
  useShowPeople,
  useShowRecommendations,
  useShowSeasonEpisodes,
  useShowSeasons,
  useShowStats,
} from '@/api/hooks/useShows'
import { Page, ScrollRow, Section } from '@/components/layout'
import {
  CastRow,
  CrewLine,
  DetailHero,
  EpisodeGrid,
  MediaActions,
  MetadataBadges,
  ReviewsSection,
  ScoreRow,
  SeasonRail,
} from '@/components/media'
import {
  Button,
  InfoPanel,
  PosterCard,
  SectionHead,
  ShimmerText,
  StatCard,
} from '@/components/ui'
import { localRowID } from '@/lib/ids'
import { firstImage } from '@/lib/images'
import { mediaLink } from '@/lib/media-url'
import { formatDate } from '@/lib/utils'

export const Route = createFileRoute('/shows/$id')({
  component: ShowDetailPage,
})

function ShowDetailPage() {
  const { id } = Route.useParams()
  const navigate = useNavigate()

  const { data: show, isLoading, isError } = useShow(id)
  const { data: people } = useShowPeople(id)
  const { data: stats } = useShowStats(id)
  const { data: seasons } = useShowSeasons(id)
  const { data: recommendations } = useShowRecommendations(id)
  const [season, setSeason] = useState<number | null>(null)

  const ordered = useMemo(
    () => [...(seasons ?? [])].sort((a, b) => a.number - b.number),
    [seasons]
  )

  // Open on the first season the catalogue holds. `null` means "not chosen
  // yet", which keeps a later refetch from yanking the reader back to season 1
  // after they picked another.
  useEffect(() => {
    if (season !== null || ordered.length === 0) return
    setSeason(ordered[0].number)
  }, [season, ordered])

  const { data: episodes, isLoading: episodesLoading } = useShowSeasonEpisodes(
    id,
    season ?? undefined
  )

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

  if (isError || !show) {
    return (
      <Page>
        <div className="flex flex-col items-center gap-4 py-24 text-center">
          <p className="text-lg font-semibold text-sf-ink">Show not found</p>
          <p className="max-w-sm text-sm text-sf-ink-muted">
            It may not be in this catalogue yet. Search for it to add it.
          </p>
          <Button variant="glass" onClick={() => navigate({ to: '/search' })}>
            Search shows
          </Button>
        </div>
      </Page>
    )
  }

  const cast = people?.cast ?? []
  const crew = people?.crew ?? []
  const rowID = localRowID(show.ids)
  const backdrop = firstImage(show.images, 'fanart', 'clearart')
  const poster = firstImage(show.images, 'poster', 'thumb')
  const firstYear = show.first_aired?.slice(0, 4)
  const lastYear = show.last_aired?.slice(0, 4)
  const yearLabel =
    firstYear && lastYear && lastYear !== firstYear
      ? `${firstYear}–${lastYear}`
      : firstYear

  return (
    <div className="pb-16">
      <DetailHero
        context={
          <>
            <span>Series</span>
            {show.network && (
              <>
                <span aria-hidden="true">·</span>
                <span>{show.network}</span>
              </>
            )}
          </>
        }
        title={show.title}
        tagline={show.tagline}
        backdrop={backdrop}
        poster={poster ?? ''}
        posterFallback={show.title}
        meta={
          <MetadataBadges
            year={yearLabel}
            certification={show.certification}
            seasonCount={ordered.length || show.season_count}
            episodeCount={show.episode_count}
            status={show.status}
          />
        }
        score={<ScoreRow rating={show.rating} votes={show.votes} />}
        overview={show.overview}
        crewLine={
          <CrewLine
            crew={crew}
            genres={show.genres}
            leadLabel="Created by"
            leadJobs={['Creator', 'Director']}
          />
        }
        actions={
          <MediaActions kind="show" ids={show.ids} trailer={show.trailer} />
        }
      />

      <Page className="pt-8">
        {ordered.length > 0 && (
          <Section>
            <SectionHead
              title="Seasons"
              action={ordered.length > 1 ? undefined : undefined}
            />
            <SeasonRail
              seasons={ordered}
              active={season ?? undefined}
              onSelect={setSeason}
              fallbackPoster={poster}
            />
          </Section>
        )}

        <Section>
          <SectionHead
            title={season ? `Season ${season} episodes` : 'Episodes'}
          />
          <EpisodeGrid
            episodes={episodes ?? []}
            seasonNumber={season ?? 1}
            isLoading={episodesLoading}
          />
        </Section>

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
            {show.first_aired && (
              <StatCard
                label="First aired"
                value={formatDate(show.first_aired)}
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
              {show.network && <Fact label="Network" value={show.network} />}
              {show.first_aired && (
                <Fact
                  label="First aired"
                  value={formatDate(show.first_aired)}
                />
              )}
              {show.last_aired && (
                <Fact label="Last aired" value={formatDate(show.last_aired)} />
              )}
              {show.status && <Fact label="Status" value={show.status} />}
              {show.language && <Fact label="Language" value={show.language} />}
              {show.certification && (
                <Fact label="Certification" value={show.certification} />
              )}
            </div>
          </InfoPanel>
        </Section>

        <ReviewsSection kind="show" rowID={rowID} />

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
                  type="series"
                  aspect="2:3"
                  onClick={() => {
                    const link = mediaLink('show', item.ids)
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
