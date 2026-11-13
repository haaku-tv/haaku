import { ChevronDown, ChevronUp } from 'lucide-react'
import { type ReactNode, useLayoutEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

export type DetailHeroProps = {
  /** Small line above the title: the media kind, and the studio or network. */
  context?: ReactNode
  title: string
  tagline?: string
  /** Wide artwork behind the hero; falls back to the poster. */
  backdrop?: string
  poster?: string
  /** Rendered in the poster frame when the title has no artwork. */
  posterFallback?: string
  /** Year, certification, runtime, seasons/episodes — the pill row. */
  meta?: ReactNode
  /** Community score line. */
  score?: ReactNode
  overview?: string
  /** "Directed by …" line. */
  crewLine?: ReactNode
  genres?: string[]
  /** Primary and secondary actions, laid out under the copy. */
  actions?: ReactNode
  /** Optional right-hand column on wide viewports. */
  aside?: ReactNode
  className?: string
}

/**
 * The hero every media page opens with: artwork behind, poster and copy in
 * front of it, actions at the bottom.
 *
 * The copy block is bottom-justified inside a minimum height rather than a
 * fixed one, so a long tagline or overview grows the hero downwards instead of
 * pushing the poster off the top of the frame on a short viewport.
 */
export function DetailHero({
  context,
  title,
  tagline,
  backdrop,
  poster,
  posterFallback,
  meta,
  score,
  overview,
  crewLine,
  genres,
  actions,
  aside,
  className,
}: DetailHeroProps) {
  const artwork = backdrop ?? poster

  return (
    <section
      className={cn(
        'relative isolate overflow-hidden border-b border-sf-line',
        className
      )}
    >
      {artwork && (
        <div className="absolute inset-0 -z-10">
          <img
            src={artwork}
            alt=""
            aria-hidden="true"
            className="h-full w-full object-cover object-[center_22%]"
          />
          {/* Left-to-right wash keeps the copy readable over bright artwork;
              the bottom fade hands the eye off to the page below. */}
          <div className="absolute inset-0 bg-linear-to-r from-sf-bg via-sf-bg/85 to-sf-bg/25" />
          <div className="absolute inset-0 bg-linear-to-t from-sf-bg via-sf-bg/40 to-transparent" />
        </div>
      )}
      {!artwork && (
        <div className="absolute inset-0 -z-10 grid-bg bg-linear-to-br from-sf-bg-3 via-sf-bg-2 to-sf-bg" />
      )}

      <div className="mx-auto flex min-h-[min(64dvh,620px)] w-full max-w-[1440px] flex-col justify-end px-5 pt-24 pb-8 sm:px-8 lg:px-10">
        {context && (
          <div className="mb-4 flex flex-wrap items-center gap-2 text-[11px] font-bold tracking-[0.18em] text-sf-ink-faint uppercase">
            {context}
          </div>
        )}

        <div className="flex flex-col gap-6 lg:flex-row lg:items-end">
          {poster !== undefined && (
            <div className="w-[132px] shrink-0 overflow-hidden rounded-sf border border-sf-line bg-sf-bg-3 shadow-[var(--sf-shadow)] sm:w-[176px] lg:w-[210px]">
              {poster ? (
                <img
                  src={poster}
                  alt={title}
                  className="aspect-[2/3] h-full w-full object-cover"
                />
              ) : (
                <div className="grid aspect-[2/3] place-items-center bg-linear-to-br from-sf-bg-3 to-sf-bg-2 p-3 text-center">
                  <span className="font-display text-3xl font-bold text-sf-ink-faint">
                    {(posterFallback ?? title).charAt(0)}
                  </span>
                </div>
              )}
            </div>
          )}

          <div className="min-w-0 max-w-3xl flex-1">
            <h1 className="title-shadow font-display text-[clamp(2rem,5.2vw,4rem)] leading-[1.02] font-bold tracking-[-0.03em] text-sf-ink">
              {title}
            </h1>

            {tagline && (
              <p className="mt-3 text-sm text-sf-ink-muted italic">{tagline}</p>
            )}

            {(meta || score) && (
              <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2">
                {meta}
                {score}
              </div>
            )}

            {overview && (
              // keyed by the copy: a different synopsis remounts the clamp so
              // the More/Less state and the measurement start over.
              <Overview key={overview} text={overview} />
            )}

            {crewLine && <div className="mt-3">{crewLine}</div>}

            {genres && genres.length > 0 && (
              <div className="mt-4 flex flex-wrap gap-2">
                {genres.map((genre) => (
                  <span key={genre} className="meta-badge">
                    {genre}
                  </span>
                ))}
              </div>
            )}

            {actions && (
              <div className="mt-6 flex flex-wrap items-center gap-2">
                {actions}
              </div>
            )}
          </div>

          {aside && <div className="lg:justify-self-end">{aside}</div>}
        </div>
      </div>
    </section>
  )
}

/**
 * Overview copy with a More/Less toggle.
 *
 * The toggle appears only when the clamp actually hides text: measuring the
 * paragraph is the difference between a disclosure control and a button that
 * does nothing on a two-line synopsis.
 */
function Overview({ text }: { text: string }) {
  const ref = useRef<HTMLParagraphElement>(null)
  const [expanded, setExpanded] = useState(false)
  const [overflowing, setOverflowing] = useState(false)

  useLayoutEffect(() => {
    const node = ref.current
    if (!node || expanded) return
    const measure = () =>
      setOverflowing(node.scrollHeight > node.clientHeight + 1)
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    return () => observer.disconnect()
  }, [expanded])

  return (
    <div className="mt-4 max-w-2xl">
      <p
        ref={ref}
        className={cn(
          'text-sm leading-7 text-sf-ink-muted sm:text-[0.9375rem]',
          !expanded && 'line-clamp-3'
        )}
      >
        {text}
      </p>
      {overflowing && (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
          className="mt-1 inline-flex cursor-pointer items-center gap-1 text-sm font-semibold text-sf-ink transition-colors hover:text-sf-accent-2"
        >
          {expanded ? (
            <ChevronUp className="size-3.5" />
          ) : (
            <ChevronDown className="size-3.5" />
          )}
          {expanded ? 'Less' : 'More'}
        </button>
      )}
    </div>
  )
}
