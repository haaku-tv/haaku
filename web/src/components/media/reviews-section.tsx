import { Section } from '@/components/layout'
import { CommentThread } from '@/components/social/CommentThread'

export type ReviewsSectionProps = {
  kind: 'movie' | 'show' | 'episode'
  /** Local row id of the title the reviews belong to. */
  rowID: number
}

/**
 * Reviews and comments for one title.
 *
 * The thread owns its own reading and posting; this section only supplies the
 * heading and the identity the comments are addressed to, so the page does not
 * need a second copy of the pagination and spoiler handling.
 */
export function ReviewsSection({ kind, rowID }: ReviewsSectionProps) {
  if (!rowID) return null

  return (
    <Section>
      <h2 className="mb-4 font-display text-xl font-semibold text-sf-ink">
        Reviews &amp; comments
      </h2>
      <CommentThread mediaType={kind} mediaId={String(rowID)} />
    </Section>
  )
}
