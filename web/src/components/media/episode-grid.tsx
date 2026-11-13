import { Link } from '@tanstack/react-router'
import { Play } from 'lucide-react'
import type { Episode } from '@/api/types'
import { localRowID } from '@/lib/ids'
import { episodeStill } from '@/lib/images'
import { formatRuntime } from './metadata-badges'

export type EpisodeGridProps = {
  episodes: Episode[]
  /** Season the rows belong to; the API also sends it per episode. */
  seasonNumber: number
  isLoading?: boolean
}

/**
 * The season's episodes as a grid of stills.
 *
 * Every card opens the episode page. An episode with no still shows a play mark
 * instead of an empty frame, because a missing screenshot is common on a title
 * the catalogue has only partly ingested and should not look like a broken
 * image.
 */
export function EpisodeGrid({
  episodes,
  seasonNumber,
  isLoading,
}: EpisodeGridProps) {
  if (isLoading) {
    return (
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {Array.from({ length: 6 }, (_, index) => (
          <div
            key={index}
            className="glass h-[168px] animate-pulse rounded-sf"
          />
        ))}
      </div>
    )
  }

  if (episodes.length === 0) {
    return (
      <div className="glass rounded-sf px-5 py-8 text-sm text-sf-ink-muted">
        No episodes are recorded for season {seasonNumber} yet.
      </div>
    )
  }

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {episodes.map((episode, index) => {
        const number = episode.number || index + 1
        const rowID = localRowID(episode.ids)
        const still = episodeStill(episode.images)
        const season = episode.season || seasonNumber

        const body = (
          <>
            <div className="relative aspect-video w-full overflow-hidden bg-sf-bg-3">
              {still ? (
                <img
                  src={still}
                  alt=""
                  loading="lazy"
                  className="h-full w-full object-cover transition-transform duration-500 group-hover:scale-[1.04]"
                />
              ) : (
                <div className="grid h-full place-items-center">
                  <Play className="size-7 text-sf-ink-hint" />
                </div>
              )}
              <div className="pointer-events-none absolute inset-0 bg-linear-to-t from-black/80 via-black/10 to-transparent" />
              <span className="absolute top-2 left-2 rounded-full bg-black/65 px-2 py-0.5 text-[10px] font-bold tracking-[0.08em] text-white backdrop-blur-sm">
                S{season} E{number}
              </span>
            </div>

            <div className="flex flex-1 flex-col gap-1 p-3.5">
              <p className="line-clamp-1 text-sm font-semibold text-sf-ink">
                {episode.title || `Episode ${number}`}
              </p>
              <p className="text-xs text-sf-ink-faint">
                {[formatRuntime(episode.runtime), episode.first_aired]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
              {episode.overview && (
                <p className="mt-1 line-clamp-2 text-xs leading-relaxed text-sf-ink-muted">
                  {episode.overview}
                </p>
              )}
            </div>
          </>
        )

        if (!rowID) {
          return (
            <div
              key={`${season}-${number}`}
              className="glass flex flex-col overflow-hidden rounded-sf"
            >
              {body}
            </div>
          )
        }

        return (
          <Link
            key={rowID}
            to="/episodes/$id"
            params={{ id: String(rowID) }}
            className="group glass flex flex-col overflow-hidden rounded-sf transition-colors hover:border-sf-line-2"
          >
            {body}
          </Link>
        )
      })}
    </div>
  )
}
