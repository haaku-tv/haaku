// @vitest-environment happy-dom

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { GET, POST } = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }))

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}))

import type { ApiClient } from '@/api/client'
import {
  useAddToList,
  useListItems,
  useListMembership,
  useMyLists,
} from './useLists'

// The hooks take the client explicitly, so a plain stub is enough — the app's
// useApi() is not involved.
const client = { GET, POST } as unknown as ApiClient

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe('list hooks', () => {
  beforeEach(() => {
    GET.mockReset()
    POST.mockReset()
  })

  it('reads list membership from the reverse-index endpoint', async () => {
    GET.mockResolvedValue({
      data: [
        { name: 'Chill', ids: { trakt: 7, slug: 'chill' }, item_count: 2 },
      ],
    })

    const { result } = renderHook(
      () => useListMembership(client, 'movie', 11),
      { wrapper }
    )

    await waitFor(() => expect(result.current.data).toHaveLength(1))
    expect(result.current.data?.[0].name).toBe('Chill')
    expect(GET).toHaveBeenCalledWith('/v1/users/me/lists/containing', {
      params: { query: { type: 'movie', media_id: '11' } },
    })
  })

  it('skips the membership read when there is no local row id', () => {
    renderHook(() => useListMembership(client, 'movie', 0), { wrapper })
    expect(GET).not.toHaveBeenCalled()
  })

  it('reads my lists whether they arrive bare or in an items envelope', async () => {
    GET.mockResolvedValue({
      data: {
        items: [{ name: 'Wrapped', ids: { trakt: 3, slug: 'wrapped' } }],
      },
    })

    const { result } = renderHook(() => useMyLists(client), { wrapper })

    await waitFor(() => expect(result.current.data).toHaveLength(1))
    expect(result.current.data?.[0].name).toBe('Wrapped')
  })

  it('reads list items as a bare array of contract item objects', async () => {
    GET.mockResolvedValue({
      data: [
        {
          id: 5,
          rank: 0,
          listed_at: '2026-11-13T00:00:00Z',
          type: 'movie',
          movie: {
            title: 'The Matrix',
            year: 1999,
            ids: { trakt: 11, slug: 'the-matrix' },
          },
        },
      ],
    })

    const { result } = renderHook(() => useListItems(client, 1), { wrapper })

    await waitFor(() => expect(result.current.data).toHaveLength(1))
    expect(result.current.data?.[0].movie?.title).toBe('The Matrix')
    expect(GET).toHaveBeenCalledWith('/v1/lists/{id}/items', {
      params: { path: { id: '1' }, query: { page: 1, limit: 20 } },
    })
  })

  it('adds items with the ids it was handed', async () => {
    POST.mockResolvedValue({ data: { added: { movie: 1 } } })

    const { result } = renderHook(() => useAddToList(client), { wrapper })

    await expect(
      result.current.mutateAsync({
        id: 4,
        payload: { movies: [{ ids: { trakt: 11, slug: 'the-matrix' } }] },
      })
    ).resolves.toBeTruthy()

    expect(POST).toHaveBeenCalledWith('/v1/lists/{id}/items', {
      params: { path: { id: '4' } },
      body: { movies: [{ ids: { trakt: 11, slug: 'the-matrix' } }] },
    })
  })
})
