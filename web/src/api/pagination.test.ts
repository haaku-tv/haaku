import { describe, expect, it } from 'vitest'
import { parsePage } from './pagination'

/**
 * The API serves a paginated collection in more than one shape, and the pages
 * read whichever one arrives. Reading only the bare array is what left the
 * watchlist/history/ratings/collection/favorites pages empty while the rows
 * were really stored: `/v1/sync/watchlist` answers
 * `{items, page, limit, row_count}`, which `Array.isArray` rejects.
 */
describe('parsePage', () => {
  const headers = (pairs: Record<string, string>) => ({
    get: (name: string) => pairs[name.toLowerCase()] ?? null,
  })

  it('reads the internal {items,page,limit,row_count} envelope', () => {
    const page = parsePage<{ id: number }>({
      data: {
        items: [{ id: 1 }, { id: 2 }],
        page: 2,
        limit: 24,
        row_count: 50,
      },
    })

    expect(page).toEqual({
      data: [{ id: 1 }, { id: 2 }],
      page: 2,
      limit: 24,
      total: 50,
      total_pages: 3,
    })
  })

  it('reads the activity {data,page,limit,total_count} envelope', () => {
    const page = parsePage({
      data: { data: [{ id: 7 }], page: 1, limit: 25, total_count: 1 },
    })

    expect(page.data).toEqual([{ id: 7 }])
    expect(page.total).toBe(1)
  })

  it('reads a bare contract array with X-Pagination-* headers', () => {
    const page = parsePage<{ rank: number }>({
      data: [{ rank: 1 }],
      response: {
        headers: headers({
          'x-pagination-page': '3',
          'x-pagination-limit': '10',
          'x-pagination-total-items': '21',
        }),
      },
    })

    expect(page.page).toBe(3)
    expect(page.limit).toBe(10)
    expect(page.total).toBe(21)
    expect(page.total_pages).toBe(3)
  })

  it('prefers the headers over the envelope when both are present', () => {
    const page = parsePage({
      data: { items: [{ id: 1 }], page: 1, limit: 25, row_count: 1 },
      response: {
        headers: headers({
          'x-pagination-page': '2',
          'x-pagination-limit': '5',
          'x-pagination-total-items': '11',
        }),
      },
    })

    expect(page.page).toBe(2)
    expect(page.total).toBe(11)
    expect(page.total_pages).toBe(3)
  })

  it('reports an empty page for an empty envelope rather than guessing', () => {
    const page = parsePage({
      data: { items: [], page: 1, limit: 24, row_count: 0 },
    })

    expect(page.data).toEqual([])
    expect(page.total).toBe(0)
    expect(page.total_pages).toBe(0)
  })

  it('tolerates a missing body (unauthenticated first render)', () => {
    expect(parsePage({ data: undefined }).data).toEqual([])
    expect(parsePage({}).total_pages).toBe(0)
  })
})
