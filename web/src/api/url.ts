/**
 * apiURL joins the configured API base with a route path.
 *
 * Both halves may carry their own slash: `VITE_API_URL` can be absolute
 * ("https://api.example.com/") or the default "/", while every caller's path
 * starts with "/". Concatenating them naively produces "//v1/movies/550", which
 * a browser reads as a PROTOCOL-RELATIVE url — host "v1" — so the request dies
 * with ERR_NAME_NOT_RESOLVED before it ever reaches this server. One builder
 * keeps that rule in a single place instead of three copies of the same string
 * template.
 */
export function apiURL(
  path: string,
  params?: Record<string, string | number | undefined>
): string {
  // A trailing slash on the base and a leading slash on the path are the same
  // separator; keep exactly one.
  const base = (import.meta.env.VITE_API_URL || '/').replace(/\/+$/, '')
  const suffix = path.startsWith('/') ? path : `/${path}`
  let url = `${base}${suffix}`

  if (params) {
    const search = new URLSearchParams()
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== '') search.set(key, String(value))
    }
    const query = search.toString()
    if (query) url += `?${query}`
  }

  return url
}
