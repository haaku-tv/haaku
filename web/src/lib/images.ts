import type { ImageSet, ImageURLs } from '@/api/types'

/**
 * firstImage picks the one URL the UI should display out of a media object's
 * `images`.
 *
 * The API serves that object in the shape the Trakt contract declares: every key
 * of the media type is present and every value is an ARRAY of URLs (the object
 * itself may be null). The UI's cards and heroes take a single URL, so this walks
 * the keys in the caller's order of preference and returns the first non-empty
 * URL it finds.
 *
 * The keys are named by the CALLER because the contract names them per type:
 * a backdrop is `fanart`, an episode's still is `screenshot`, a person's portrait
 * is `headshot`, and a list's cover is the plural `posters`. Ordering is the
 * caller's too — `firstImage(show.images, 'fanart', 'poster')` reads the wide
 * backdrop first and falls back to the square poster.
 *
 * A scalar value is accepted as well as an array: a stored row, and a response
 * written before the shapes were normalised, can hold one.
 */
export function firstImage(
  images: ImageSet | null | undefined,
  ...keys: (keyof ImageSet)[]
): string | undefined {
  if (!images) return undefined
  for (const key of keys) {
    const value = (images as Record<string, ImageURLs>)[key]
    if (Array.isArray(value)) {
      const hit = value.find((v) => typeof v === 'string' && v !== '')
      if (hit) return hit
      continue
    }
    if (typeof value === 'string' && value !== '') return value
  }
  return undefined
}

/**
 * episodeStill is the thumbnail an episode row shows.
 *
 * An episode's still is the contract's `screenshot`; `poster` and `thumb` are
 * read after it because they are the names an older row — and the pre-contract
 * response — carried the same image under.
 */
export function episodeStill(
  images: ImageSet | null | undefined
): string | undefined {
  return firstImage(images, 'screenshot', 'poster', 'thumb')
}
