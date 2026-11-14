import { Star } from 'lucide-react'
import { cn } from '@/lib/utils'

export type ScoreRowProps = {
  /** Community score on the API's 0–10 scale, or 0 when nobody voted. */
  rating?: number
  votes?: number
  /** The signed-in user's own score, 0 when unrated. */
  userRating?: number
  /** Called with 1–10; omit to render the user's score read-only. */
  onRate?: (rating: number) => void
  className?: string
}

/**
 * Two scores side by side: what everyone rated the title (/10), and what this
 * person rated it (an editable row of stars).
 *
 * The community score is skipped when there are no votes rather than shown as
 * "0.0", which reads as a bad rating instead of no rating.
 */
export function ScoreRow({
  rating,
  votes,
  userRating = 0,
  onRate,
  className,
}: ScoreRowProps) {
  const hasCommunity = typeof rating === 'number' && rating > 0
  const showUser = onRate !== undefined || userRating > 0

  return (
    <div
      className={cn('flex flex-wrap items-center gap-x-5 gap-y-3', className)}
    >
      {hasCommunity && (
        <span className="inline-flex items-center gap-2">
          <Star className="size-4 fill-sf-accent-2 text-sf-accent-2" />
          <span className="font-display text-base font-bold text-sf-ink tabular-nums">
            {rating.toFixed(1)}
            <span className="text-xs font-semibold text-sf-ink-faint">/10</span>
          </span>
          {votes ? (
            <span className="text-xs text-sf-ink-faint tabular-nums">
              {compactNumber(votes)} votes
            </span>
          ) : null}
        </span>
      )}

      {showUser && (
        <span className="inline-flex items-center gap-1.5">
          <span className="mr-1 text-[11px] font-bold tracking-[0.14em] text-sf-ink-faint uppercase">
            Your rating
          </span>
          <UserRating value={userRating} onRate={onRate} />
        </span>
      )}
    </div>
  )
}

function UserRating({
  value,
  onRate,
}: {
  value: number
  onRate?: (rating: number) => void
}) {
  if (!onRate) {
    return value > 0 ? (
      <span className="text-sm font-semibold text-sf-ink-muted tabular-nums">
        {value}/10
      </span>
    ) : null
  }

  return (
    <span className="flex items-center gap-0.5">
      {Array.from({ length: 10 }, (_, index) => index + 1).map((score) => (
        <button
          key={score}
          type="button"
          onClick={() => onRate(score)}
          aria-label={`Rate ${score} out of 10`}
          className="cursor-pointer p-0.5 transition-transform hover:scale-110"
        >
          <Star
            className={cn(
              'size-3.5 transition-colors',
              score <= value
                ? 'fill-sf-accent-2 text-sf-accent-2'
                : 'text-sf-ink-hint hover:text-sf-ink-faint'
            )}
          />
        </button>
      ))}
    </span>
  )
}

function compactNumber(value: number): string {
  if (value < 1000) return String(value)
  return new Intl.NumberFormat(undefined, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)
}
