import type { PaginatedResponse } from '@/api/types'

/**
 * One paginated collection, in whichever of the API's shapes carried it.
 *
 * The API serves paginated reads in TWO shapes and the pages read both:
 *
 *  - Trakt's contract paths (`/v1/sync/watchlist/movies/rank/asc`,
 *    `/v1/users/{id}/watchlist/movies`, …) return a BARE ARRAY with the
 *    `X-Pagination-*` headers: page / limit / total-items / total-pages.
 *  - Our internal paths (`/v1/sync/watchlist`, `/v1/sync/history`, …) return an
 *    `{items, page, limit, row_count}` envelope. `/v1/users/me/activity` uses
 *    `{data, page, limit, total_count}`.
 *
 * A reader that only understands the bare array sees `undefined` for the
 * envelope and renders "empty" for a list that has rows — which is exactly what
 * happened to the watchlist, history, ratings, collection and favorites pages.
 * Read the body first, fall back to the headers, and never assume one shape.
 */
export function parsePage<T>(
  response: {
    data?: unknown
    response?: { headers?: { get(name: string): string | null } } | null
  },
  fallbackLimit = 20
): PaginatedResponse<T> {
  const body = response?.data
  const headers = response?.response?.headers

  const envelope: Record<string, unknown> =
    body && typeof body === 'object' && !Array.isArray(body)
      ? (body as Record<string, unknown>)
      : {}

  const rows = firstArray(body, envelope.items, envelope.data)
  const total =
    firstNumber(
      headerValue(headers, 'x-pagination-total-items'),
      envelope.row_count,
      envelope.total_count,
      envelope.total
    ) ?? rows.length
  const page =
    firstNumber(headerValue(headers, 'x-pagination-page'), envelope.page) ?? 1
  const limit =
    firstNumber(headerValue(headers, 'x-pagination-limit'), envelope.limit) ??
    fallbackLimit

  return {
    data: rows as T[],
    page,
    limit,
    total,
    total_pages: limit > 0 ? Math.ceil(total / limit) : 0,
  }
}

function firstArray(...candidates: unknown[]): unknown[] {
  for (const candidate of candidates) {
    if (Array.isArray(candidate)) return candidate
  }
  return []
}

function firstNumber(...candidates: unknown[]): number | undefined {
  for (const candidate of candidates) {
    if (candidate === null || candidate === undefined || candidate === '') {
      continue
    }
    const value = Number(candidate)
    if (Number.isFinite(value)) return value
  }
  return undefined
}

function headerValue(
  headers: { get(name: string): string | null } | undefined | null,
  name: string
): string | null | undefined {
  if (!headers) return undefined
  return headers.get(name)
}
