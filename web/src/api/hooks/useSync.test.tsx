// @vitest-environment happy-dom

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

// The hooks reach the API through `useApi`, which reads the auth context, so
// Clerk is stubbed to a signed-in identity and fetch to a canned response.
vi.mock('@clerk/react', () => ({
  useAuth: () => ({ getToken: async () => 'test-token', isSignedIn: true }),
}))
vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}))

import { AuthContext, type AuthContextValue } from '@/auth/context'
import { useWatchlist } from './useSync'

// `useApi` reads the auth context, which the app shell provides in production.
const authValue: AuthContextValue = {
  mode: 'local',
  config: null,
  isLoaded: true,
  isSignedIn: true,
  user: { id: 'local_testuser', email: null, username: 'tester' },
  isAdmin: false,
  getToken: async () => 'test-token',
  signOut: async () => {},
  setSession: () => {},
}

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <AuthContext.Provider value={authValue}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </AuthContext.Provider>
  )
}

/**
 * `/v1/sync/watchlist` answers `{items, page, limit, row_count}` — an envelope,
 * not the bare array the contract paths serve. The hook treated anything that
 * was not an array as "no data", so the watchlist page showed its empty state
 * with rows sitting in the database.
 */
describe('useWatchlist — response shape', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
  })

  it('unwraps the {items,row_count} envelope', async () => {
    vi.stubEnv('VITE_API_URL', 'http://api.test')
    const item = {
      id: 3,
      rank: 1,
      listed_at: '2026-11-13T16:02:30.689Z',
      type: 'movie',
      movie: { title: 'Inception', ids: { tmdb: 27205 } },
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              items: [item],
              page: 1,
              limit: 24,
              row_count: 41,
            }),
            {
              status: 200,
              headers: { 'content-type': 'application/json' },
            }
          )
      )
    )

    const { result } = renderHook(() => useWatchlist({ limit: 24 }), {
      wrapper,
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.data).toHaveLength(1)
    expect(result.current.data?.data[0]?.movie?.title).toBe('Inception')
    // row_count, not the page length, is the total.
    expect(result.current.data?.total).toBe(41)
    expect(result.current.data?.total_pages).toBe(2)
  })
})
