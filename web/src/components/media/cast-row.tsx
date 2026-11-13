import { Link } from '@tanstack/react-router'
import type { CastMember } from '@/api/types'
import { localRowID } from '@/lib/ids'
import { firstImage } from '@/lib/images'

export type CastRowProps = {
  cast: CastMember[]
  limit?: number
}

/**
 * Cast and crew portraits, as a scrollable row.
 *
 * A person without a headshot gets their initials rather than the poster-card
 * letter placeholder, because a row of identical first letters reads as broken
 * artwork while a monogram reads as "no photo on file".
 */
export function CastRow({ cast, limit = 16 }: CastRowProps) {
  const people = cast.slice(0, limit)
  if (people.length === 0) return null

  return (
    <div className="scrollbar-hide -mx-1 flex gap-4 overflow-x-auto px-1 pb-1">
      {people.map((member, index) => {
        const name = member.person?.name ?? 'Unknown'
        const rowID = localRowID(member.person?.ids)
        const headshot = firstImage(member.person?.images, 'headshot', 'fanart')

        const card = (
          <>
            <div className="aspect-square w-full overflow-hidden rounded-sf border border-sf-line bg-sf-bg-3">
              {headshot ? (
                <img
                  src={headshot}
                  alt={name}
                  loading="lazy"
                  className="h-full w-full object-cover transition-transform duration-500 group-hover:scale-[1.05]"
                />
              ) : (
                <div className="grid h-full place-items-center bg-linear-to-br from-sf-bg-3 to-sf-bg-2">
                  <span className="font-display text-2xl font-bold text-sf-ink-faint">
                    {initials(name)}
                  </span>
                </div>
              )}
            </div>
            <p className="mt-2 line-clamp-2 text-xs font-semibold text-sf-ink">
              {name}
            </p>
            {member.character && (
              <p className="line-clamp-1 text-[11px] text-sf-ink-faint">
                {member.character}
              </p>
            )}
          </>
        )

        const className = 'group w-[112px] shrink-0 sm:w-[124px]'

        return rowID ? (
          <Link
            key={`${rowID}-${index}`}
            to="/people/$id"
            params={{ id: String(rowID) }}
            className={className}
          >
            {card}
          </Link>
        ) : (
          <div key={`${name}-${index}`} className={className}>
            {card}
          </div>
        )
      })}
    </div>
  )
}

function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part.charAt(0).toUpperCase())
    .join('')
}
