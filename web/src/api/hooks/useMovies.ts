/**
 * Movie hooks — powered by TanStack Query
 */

import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/fetch'
import { getMedia } from '@/api/media'
import type {
  Alias,
  CommentResponse,
  CreditResponse,
  MediaStats,
  Movie,
  RatingDistribution,
  TraktList,
  TrendingItem,
  WatchProviderCountry,
} from '@/api/types'

// ─── Keys ──────────────────────────────────────────────────────────────

export const movieKeys = {
  all: ['movies'] as const,
  detail: (id: string | number) => ['movies', 'detail', String(id)] as const,
  people: (id: string | number) => ['movies', 'people', String(id)] as const,
  stats: (id: string | number) => ['movies', 'stats', String(id)] as const,
  ratings: (id: string | number) => ['movies', 'ratings', String(id)] as const,
  watch: (id: string | number) => ['movies', 'watch', String(id)] as const,
  lists: (id: string | number) => ['movies', 'lists', String(id)] as const,
  watching: (id: string | number) =>
    ['movies', 'watching', String(id)] as const,
  aliases: (id: string | number) => ['movies', 'aliases', String(id)] as const,
  recommendations: (id: string | number) =>
    ['movies', 'recommendations', String(id)] as const,
  comments: (id: string | number) =>
    ['movies', 'comments', String(id)] as const,
  trending: (page?: number) => ['movies', 'trending', page ?? 1] as const,
  popular: (page?: number) => ['movies', 'popular', page ?? 1] as const,
}

// ─── Hooks ─────────────────────────────────────────────────────────────

export function useMovie(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.detail(id ?? ''),
    queryFn: () => getMedia<Movie>('movies', id),
    enabled: !!id,
    staleTime: 5 * 60_000,
  })
}

export function useMoviePeople(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.people(id ?? ''),
    queryFn: () => getMedia<CreditResponse>('movies', id, '/people'),
    enabled: !!id,
    staleTime: 5 * 60_000,
  })
}

export function useMovieStats(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.stats(id ?? ''),
    queryFn: () => getMedia<MediaStats>('movies', id, '/stats'),
    enabled: !!id,
    staleTime: 2 * 60_000,
  })
}

export function useMovieRatings(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.ratings(id ?? ''),
    queryFn: () => getMedia<RatingDistribution>('movies', id, '/ratings'),
    enabled: !!id,
    staleTime: 2 * 60_000,
  })
}

export function useMovieWatchProviders(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.watch(id ?? ''),
    queryFn: () => getMedia<WatchProviderCountry[]>('movies', id, '/watch'),
    enabled: !!id,
    staleTime: 10 * 60_000,
  })
}

export function useMovieLists(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.lists(id ?? ''),
    queryFn: () => getMedia<TraktList[]>('movies', id, '/lists'),
    enabled: !!id,
    staleTime: 2 * 60_000,
  })
}

export function useMovieWatching(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.watching(id ?? ''),
    queryFn: () => getMedia<unknown[]>('movies', id, '/watching'),
    enabled: !!id,
    staleTime: 30_000,
  })
}

export function useMovieAliases(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.aliases(id ?? ''),
    queryFn: () => getMedia<Alias[]>('movies', id, '/aliases'),
    enabled: !!id,
    staleTime: 10 * 60_000,
  })
}

export function useMovieRecommendations(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.recommendations(id ?? ''),
    queryFn: () => getMedia<Movie[]>('movies', id, '', 'recommendations/'),
    enabled: !!id,
    staleTime: 5 * 60_000,
  })
}

export function useMovieComments(id: string | number | undefined) {
  return useQuery({
    queryKey: movieKeys.comments(id ?? ''),
    queryFn: () => getMedia<CommentResponse>('movies', id, '/comments'),
    enabled: !!id,
    staleTime: 60_000,
  })
}

export function useTrendingMovies(page = 1, limit = 20) {
  return useQuery({
    queryKey: movieKeys.trending(page),
    queryFn: () =>
      api.get<TrendingItem[]>(`/v1/movies/trending`, { page, limit }),
    staleTime: 60_000,
  })
}

export function usePopularMovies(page = 1, limit = 20) {
  return useQuery({
    queryKey: movieKeys.popular(page),
    queryFn: () => api.get<Movie[]>(`/v1/movies/popular`, { page, limit }),
    staleTime: 60_000,
  })
}
