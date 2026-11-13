// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import type { Ids } from '@/api/types'
import {
  isNumericID,
  localMediaID,
  mediaID,
  mediaLink,
  rowIDFromParam,
} from '@/lib/media-url'

// A media URL carries the TMDB id, not the local row id: `/shows/3` names a
// different title on every install and stops meaning anything after a catalogue
// rebuild, while `/shows/4194` is the identity the catalogue is ingested from.

function ids(overrides: Partial<Ids> = {}): Ids {
  return {
    trakt: 3,
    id: 3,
    slug: 'star-wars-the-clone-wars',
    tmdb: 4194,
    ...overrides,
  }
}

describe('mediaID', () => {
  it('prefers the TMDB id over the local row id', () => {
    expect(mediaID(ids())).toBe('4194')
  })

  it('falls back to the slug when the row has no TMDB id', () => {
    expect(mediaID(ids({ tmdb: 0 }))).toBe('star-wars-the-clone-wars')
  })

  it('falls back to the explicit local-id segment when there is neither', () => {
    // Not a bare number: a bare number is a TMDB id. `local-3` says which
    // namespace the number belongs to.
    expect(mediaID(ids({ tmdb: undefined, slug: '' }))).toBe('local-3')
  })

  it('has nothing to offer for a row with no ids at all', () => {
    expect(mediaID({ trakt: 0, id: 0, slug: '' })).toBeUndefined()
    expect(mediaID(undefined)).toBeUndefined()
  })
})

describe('localMediaID', () => {
  it('spells a row id as its own namespace', () => {
    expect(localMediaID(3)).toBe('local-3')
  })
})

describe('mediaLink', () => {
  it('links a show by TMDB id', () => {
    expect(mediaLink('show', ids())).toEqual({ to: '/shows/$id', id: '4194' })
  })

  it('links a movie by TMDB id', () => {
    expect(mediaLink('movie', ids({ tmdb: 603 }))).toEqual({
      to: '/movies/$id',
      id: '603',
    })
  })

  it('has no target without an id', () => {
    expect(mediaLink('show', { trakt: 0, id: 0, slug: '' })).toBeUndefined()
  })
})

describe('rowIDFromParam', () => {
  it('reads the row id out of the local segment, and nothing else', () => {
    expect(rowIDFromParam('local-3')).toBe('3')
    expect(rowIDFromParam('4194')).toBeNull()
    expect(rowIDFromParam('star-wars-the-clone-wars')).toBeNull()
  })
})

describe('isNumericID', () => {
  it('distinguishes an id from a slug', () => {
    expect(isNumericID('4194')).toBe(true)
    expect(isNumericID(4194)).toBe(true)
    expect(isNumericID('star-wars-the-clone-wars')).toBe(false)
    expect(isNumericID(undefined)).toBe(false)
  })
})
