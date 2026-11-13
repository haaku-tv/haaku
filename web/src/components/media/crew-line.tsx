import { Link } from '@tanstack/react-router'
import { Fragment } from 'react'
import type { CrewMember } from '@/api/types'
import { localRowID } from '@/lib/ids'

export type CrewLineProps = {
  crew: CrewMember[]
  genres?: string[]
  /** Label for the leading credit; shows lead with "Created by". */
  leadLabel?: string
  /** Jobs that can fill the leading credit, in order of preference. */
  leadJobs?: readonly string[]
}

/**
 * One line under the synopsis: who made it, and what it is.
 *
 * The leading credit is the first job in `leadJobs` that actually has credits,
 * so a series without a Creator credit still shows its directors instead of an
 * empty label, and a title with no crew still shows its genres rather than
 * vanishing entirely.
 */
export function CrewLine({
  crew,
  genres = [],
  leadLabel = 'Directed by',
  leadJobs = ['Director'],
}: CrewLineProps) {
  const leadJob = leadJobs.find((job) => crew.some((c) => c.job === job))
  const leads = pick(crew, leadJob ? [leadJob] : [], 2)
  const writers = pick(crew, ['Writer', 'Screenplay'], 2)

  const parts: { label?: string; people: { name: string; id: number }[] }[] = []
  if (leads.length > 0) parts.push({ label: leadLabel, people: leads })
  if (writers.length > 0) parts.push({ label: 'Written by', people: writers })

  const hasPeople = parts.length > 0
  if (!hasPeople && genres.length === 0) return null

  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.8125rem] text-sf-ink-muted">
      {parts.map((part, partIndex) => (
        <Fragment key={part.label}>
          {partIndex > 0 && <Dot />}
          <span className="text-sf-ink-faint">{part.label}</span>
          {part.people.map((person, index) => (
            <Fragment key={`${part.label}-${person.id}-${person.name}`}>
              {index > 0 && <span className="text-sf-ink-hint">,</span>}
              {person.id > 0 ? (
                <Link
                  to="/people/$id"
                  params={{ id: String(person.id) }}
                  className="font-medium text-sf-ink-muted transition-colors hover:text-sf-ink"
                >
                  {person.name}
                </Link>
              ) : (
                <span className="font-medium text-sf-ink-muted">
                  {person.name}
                </span>
              )}
            </Fragment>
          ))}
        </Fragment>
      ))}

      {hasPeople && genres.length > 0 && <Dot />}

      {genres.map((genre, index) => (
        <Fragment key={genre}>
          {index > 0 && <Dot />}
          <span className="text-sf-ink-muted">{genre}</span>
        </Fragment>
      ))}
    </div>
  )
}

function Dot() {
  return (
    <span aria-hidden="true" className="text-sf-ink-hint">
      ·
    </span>
  )
}

function pick(
  crew: CrewMember[],
  jobs: string[],
  limit: number
): { name: string; id: number }[] {
  const seen = new Set<string>()
  const out: { name: string; id: number }[] = []
  for (const job of jobs) {
    for (const member of crew) {
      if (member.job !== job) continue
      const name = member.person?.name ?? ''
      if (name === '' || seen.has(name)) continue
      seen.add(name)
      out.push({ name, id: localRowID(member.person?.ids) })
      if (out.length >= limit) return out
    }
  }
  return out
}
