import { describe, expect, it } from 'vitest'
import { listRowID, localRowID } from './ids'

describe('listRowID', () => {
  it('reads the contract key', () => {
    expect(listRowID({ ids: { trakt: 12, slug: 'chill' } })).toBe(12)
  })

  // The My Lists page used to read list.ids.trakt directly, so an endpoint that
  // answered the stored row (no ids at all) threw and the error boundary
  // replaced the page. The fallback keeps a shape regression to a dead link.
  it('falls back to a bare row id', () => {
    expect(listRowID({ id: 9 })).toBe(9)
  })

  it('is 0 for a row that carries neither', () => {
    expect(listRowID({})).toBe(0)
    expect(listRowID(null)).toBe(0)
    expect(listRowID(undefined)).toBe(0)
  })

  it('prefers the contract key over the bare row id', () => {
    expect(listRowID({ ids: { trakt: 4 }, id: 99 })).toBe(4)
  })
})

describe('localRowID', () => {
  it('reads trakt, then id', () => {
    expect(localRowID({ trakt: 3, slug: 'x' })).toBe(3)
    expect(localRowID({ trakt: 0, slug: 'x', id: 5 })).toBe(5)
    expect(localRowID(undefined)).toBe(0)
  })
})
