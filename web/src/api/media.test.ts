// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { getMedia } from '@/api/media'

// The media URL carries the TMDB id, so every read has to say so: the API's
// bare-numeric form addresses the LOCAL row id. Each id form is its own
// namespace and is asked for exactly once — a failed TMDB lookup is never
// retried as a row id, because a collision would open an unrelated title.

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function notFoundResponse() {
  return jsonResponse(
    { error: { code: 'not_found', message: 'media not found' } },
    404
  )
}

const fetchMock = vi.fn()

// The client prefixes the path with BASE_URL ('/' in tests), so normalise the
// doubled slash away and compare the path the API actually receives.
function urlsRequested(): string[] {
  return fetchMock.mock.calls.map((call) =>
    String(call[0]).replace(/^\/+/, '/')
  )
}

describe('getMedia', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    fetchMock.mockReset()
  })

  it('asks for the TMDB id when the route param is numeric', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ title: 'The Clone Wars' }))
    vi.stubGlobal('fetch', fetchMock)

    const show = await getMedia<{ title: string }>('shows', '4194')

    expect(show.title).toBe('The Clone Wars')
    expect(urlsRequested()).toEqual(['/v1/shows/4194?id_type=tmdb'])
  })

  it('keeps the sub-resource path after the query string', async () => {
    fetchMock.mockResolvedValue(jsonResponse([]))
    vi.stubGlobal('fetch', fetchMock)

    await getMedia('shows', '4194', '/seasons/1/episodes')

    expect(urlsRequested()).toEqual([
      '/v1/shows/4194/seasons/1/episodes?id_type=tmdb',
    ])
  })

  it('sends no id_type for a slug, which the resolver reads directly', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ title: 'Severance' }))
    vi.stubGlobal('fetch', fetchMock)

    await getMedia('shows', 'severance')

    expect(urlsRequested()).toEqual(['/v1/shows/severance'])
  })

  // A row id is spelled as its own segment, so it is never confused with a TMDB
  // id: /shows/local-3 asks for row 3, and no failed lookup is retried under a
  // different namespace (which would open an unrelated title on a collision).
  it('reads local-<n> as the row id, with no id_type and no retry', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ title: 'The Clone Wars' }))
    vi.stubGlobal('fetch', fetchMock)

    const show = await getMedia<{ title: string }>('shows', 'local-3')

    expect(show.title).toBe('The Clone Wars')
    expect(urlsRequested()).toEqual(['/v1/shows/3'])
  })

  it('does not retry a failed TMDB lookup under the row-id namespace', async () => {
    fetchMock.mockResolvedValue(notFoundResponse())
    vi.stubGlobal('fetch', fetchMock)

    await expect(getMedia('shows', '3')).rejects.toThrow()
    expect(urlsRequested()).toEqual(['/v1/shows/3?id_type=tmdb'])
  })
})
