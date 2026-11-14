import { cn } from '@/lib/utils'

export type MetadataBadgesProps = {
  year?: string | number
  certification?: string
  /** Runtime in minutes. */
  runtime?: number
  seasonCount?: number
  episodeCount?: number
  status?: string
  className?: string
}

/**
 * The pill row under a title: year, certification, length, counts, status.
 *
 * Every value is optional and an absent one renders nothing — a title with no
 * certification must not print an empty pill, and a movie must not print a
 * season count of zero.
 */
export function MetadataBadges({
  year,
  certification,
  runtime,
  seasonCount,
  episodeCount,
  status,
  className,
}: MetadataBadgesProps) {
  const runtimeLabel = formatRuntime(runtime)
  const seasonLabel = plural(seasonCount, 'Season')
  const episodeLabel = plural(episodeCount, 'Episode')

  const badges = [
    year ? String(year) : '',
    certification ?? '',
    runtimeLabel,
    seasonLabel,
    episodeLabel,
    status ?? '',
  ].filter((badge) => badge !== '')

  if (badges.length === 0) return null

  return (
    <div className={cn('flex flex-wrap items-center gap-2', className)}>
      {badges.map((badge) => (
        <span key={badge} className="meta-badge">
          {badge}
        </span>
      ))}
    </div>
  )
}

/** formatRuntime renders minutes as "2h 19m" / "45m", or "" when unknown. */
export function formatRuntime(minutes: number | undefined): string {
  if (!minutes || minutes <= 0) return ''
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  if (hours === 0) return `${rest}m`
  if (rest === 0) return `${hours}h`
  return `${hours}h ${rest}m`
}

function plural(count: number | undefined, word: string): string {
  if (!count || count <= 0) return ''
  return `${count} ${word}${count === 1 ? '' : 's'}`
}
