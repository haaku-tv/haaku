import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/fetch'

export interface Genre {
  name: string
  slug: string
}

/**
 * The genre vocabulary for one media kind.
 *
 * The instance answers this from the genres its own rows carry, so a
 * self-hosted box with no metadata token still offers a usable list; the
 * provider only fills in when the catalogue is empty. Cached for half an hour:
 * genres change when titles are ingested, not between page views.
 */
export function useGenres(kind: 'movies' | 'shows') {
  return useQuery({
    queryKey: ['genres', kind],
    queryFn: () => api.get<Genre[]>(`/v1/genres/${kind}`),
    staleTime: 30 * 60_000,
  })
}
