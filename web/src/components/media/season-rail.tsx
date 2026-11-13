import type { Season } from '@/api/types'
import { localRowID } from '@/lib/ids'
import { cn } from '@/lib/utils'

export type SeasonRailProps = {
  seasons: Season[]
  /** Season currently shown in the episode grid. */
  active?: number
  onSelect: (seasonNumber: number) => void
  /** Poster shown for a season the catalogue has no artwork for. */
  fallbackPoster?: string
}

/**
 * The season switcher: one card per season, horizontally scrollable.
 *
 * Seasons are numbered (not identified by row id) in the UI, because that is
 * how people talk about them and how the episode route addresses them. A
 * season with no artwork of its own borrows the show's poster, which keeps the
 * rail from collapsing into a row of empty frames.
 */
export function SeasonRail({
  seasons,
  active,
  onSelect,
  fallbackPoster,
}: SeasonRailProps) {
  if (seasons.length === 0) return null

  const ordered = [...seasons].sort((a, b) => a.number - b.number)

  return (
    <div className="scrollbar-hide -mx-1 flex gap-3 overflow-x-auto px-1 pb-1">
      {ordered.map((season) => {
        const poster =
          firstUrl(season.images, 'poster') ?? fallbackPoster ?? undefined
        const isActive = season.number === active
        const count = season.episode_count ?? 0

        return (
          <button
            key={localRowID(season.ids) || season.number}
            type="button"
            onClick={() => onSelect(season.number)}
            aria-pressed={isActive}
            className={cn(
              'group w-[124px] shrink-0 cursor-pointer text-left',
              isActive ? 'text-sf-ink' : 'text-sf-ink-muted'
            )}
          >
            <div
              className={cn(
                'relative overflow-hidden rounded-sf border bg-sf-bg-3 transition-all',
                isActive
                  ? 'border-sf-accent shadow-[0_0_0_1px_var(--sf-accent)]'
                  : 'border-sf-line group-hover:border-sf-line-2'
              )}
            >
              {poster ? (
                <img
                  src={poster}
                  alt=""
                  aria-hidden="true"
                  loading="lazy"
                  className="aspect-[2/3] h-full w-full object-cover transition-transform duration-300 group-hover:scale-[1.04]"
                />
              ) : (
                <div className="grid aspect-[2/3] place-items-center bg-linear-to-br from-sf-bg-3 to-sf-bg-2">
                  <span className="font-display text-2xl font-bold text-sf-ink-faint">
                    {season.number}
                  </span>
                </div>
              )}
              <div className="pointer-events-none absolute inset-0 bg-linear-to-t from-black/90 via-black/25 to-transparent" />
              <div className="absolute inset-x-2.5 bottom-2">
                <p className="text-sm font-bold text-white">
                  Season {season.number}
                </p>
                {count > 0 && (
                  <p className="text-[11px] text-white/70">
                    {count} {count === 1 ? 'episode' : 'episodes'}
                  </p>
                )}
              </div>
            </div>
          </button>
        )
      })}
    </div>
  )
}

function firstUrl(
  images: Season['images'],
  key: 'poster' | 'thumb'
): string | undefined {
  const value = images?.[key]
  if (Array.isArray(value)) return value.find((url) => url !== '')
  return undefined
}
