/**
 * `<AuthProvider>` — mode discovery + the one place that touches Clerk.
 *
 * Boot order (requirement 1):
 *   1. `GET /v1/auth/config` is fetched once, before anything renders.
 *   2. `mode:"clerk"` mounts the EXACT ClerkProvider that shipped before and
 *      feeds the shared context from Clerk's hooks — unchanged behaviour.
 *   3. `mode:"local"` mounts no ClerkProvider at all; the context is fed by the
 *      local session store, so Clerk is inert.
 *
 * If the config request fails we degrade to a heuristic (Clerk if a publishable
 * key is built in, otherwise local) rather than white-screening the app.
 */

import { ClerkProvider, useAuth, useClerk } from '@clerk/react'
import {
  type PropsWithChildren,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { logout } from '@/api/local-auth'
import { fallbackConfig, fetchAuthConfig } from './config'
import { AuthContext, type AuthContextValue } from './context'
import {
  clearSession,
  readSession,
  subscribeSession,
  writeSession,
} from './session-store'
import type { AuthConfig, AuthUser } from './types'

const clerkPubKey = import.meta.env.VITE_CLERK_PUBLISHABLE_KEY
const clerkConfigured = Boolean(
  clerkPubKey && !clerkPubKey.includes('YOUR_CLERK')
)
const clerkProxyUrl = import.meta.env.VITE_CLERK_PROXY_URL || undefined

/**
 * `is_admin` from the API, for clerk mode.
 *
 * Identity is Clerk's, but ADMINISTRATION is the instance's: `GET /v1/users/me`
 * is the only thing that knows whether this account may administer this box.
 * The bridge used to hard-code `isAdmin: false`, so an owner signing in through
 * Clerk could never reach /admin while the same account in local mode could —
 * a local/clerk divergence with no server-side counterpart.
 *
 * The request is a single read, guarded by a cancel flag; a failure leaves the
 * flag false, and every admin route re-checks the permission server-side.
 */
function useInstanceAdmin(
  isSignedIn: boolean,
  userId: string | null,
  getToken: () => Promise<string | null>
): boolean {
  const [isAdmin, setIsAdmin] = useState(false)

  useEffect(() => {
    let cancelled = false
    // Reset first: a stale `true` must never survive an account change, and it
    // must not survive a lookup that fails for the new account either.
    setIsAdmin(false)
    if (!isSignedIn || !userId) {
      return
    }
    void (async () => {
      try {
        const token = await getToken()
        if (!token) return
        const base = (import.meta.env.VITE_API_URL || '').replace(/\/$/, '')
        const res = await fetch(`${base}/v1/users/me`, {
          headers: { authorization: `Bearer ${token}` },
        })
        if (!res.ok) return
        const body = (await res.json()) as { is_admin?: boolean }
        if (!cancelled) setIsAdmin(Boolean(body?.is_admin))
      } catch {
        // Leave false: the app still works, and every admin route re-checks.
      }
    })()
    return () => {
      cancelled = true
    }
  }, [isSignedIn, userId, getToken])

  return isAdmin
}

/** Feed the shared context from Clerk — only ever mounted under ClerkProvider. */
function ClerkAuthBridge({
  config,
  children,
}: PropsWithChildren<{ config: AuthConfig | null }>) {
  const { isSignedIn, getToken } = useAuth()
  const { user, signOut } = useClerk()
  const isAdmin = useInstanceAdmin(
    Boolean(isSignedIn),
    user?.id ?? null,
    getToken
  )

  const mappedUser: AuthUser | null = user
    ? {
        id: user.id,
        email: user.primaryEmailAddress?.emailAddress ?? null,
        username: user.username ?? null,
        displayName: user.fullName ?? null,
        imageUrl: user.imageUrl ?? null,
        // Mirrors the same instance-level flag as the context below, so a
        // consumer reading user.is_admin cannot disagree with isAdmin.
        is_admin: isAdmin,
      }
    : null

  const value: AuthContextValue = useMemo(
    () => ({
      mode: 'clerk',
      config,
      isLoaded: true,
      isSignedIn: Boolean(isSignedIn),
      user: mappedUser,
      isAdmin,
      getToken: () => getToken(),
      signOut: async () => {
        await signOut()
      },
      // Clerk owns its own session lifecycle; these are inert here.
      setSession: () => {},
    }),
    [config, isSignedIn, mappedUser, isAdmin, getToken, signOut]
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

/** Feed the shared context from the local session store. */
function LocalAuthBridge({
  config,
  children,
}: PropsWithChildren<{ config: AuthConfig | null }>) {
  const [session, setSessionState] = useState(() => readSession())

  useEffect(() => subscribeSession(setSessionState), [])

  const getToken = useCallback(async () => readSession()?.token ?? null, [])

  const signOut = useCallback(async () => {
    const token = readSession()?.token ?? null
    try {
      await logout(token)
    } catch {
      // Revocation is best-effort; the client token is discarded regardless.
    }
    clearSession()
  }, [])

  const setSession = useCallback((next: ReturnType<typeof readSession>) => {
    if (next) writeSession(next)
    else clearSession()
  }, [])

  const value: AuthContextValue = useMemo(
    () => ({
      mode: 'local',
      config,
      isLoaded: true,
      isSignedIn: Boolean(session),
      user: session?.user ?? null,
      isAdmin: Boolean(session?.user?.is_admin),
      getToken,
      signOut,
      setSession,
    }),
    [config, session, getToken, signOut, setSession]
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function AuthProvider({ children }: PropsWithChildren) {
  const [config, setConfig] = useState<AuthConfig | null>(null)

  useEffect(() => {
    let cancelled = false
    fetchAuthConfig()
      .then((next) => {
        if (!cancelled) setConfig(next)
      })
      .catch(() => {
        // Degrade instead of blocking the whole app on a transient outage.
        if (!cancelled) {
          setConfig({
            local_auth: !clerkConfigured,
            first_run: false,
            password_kdf: 'argon2id',
            password_reset_delivery: 'admin_handover',
            session: {
              token_prefix: 'hs_',
              transport: 'bearer',
              ttl_seconds: 0,
            },
            ...fallbackConfig(clerkConfigured),
          })
        }
      })
    return () => {
      cancelled = true
    }
  }, [])

  if (!config) {
    return (
      <div className="grid min-h-screen place-items-center text-sm text-sf-ink-muted">
        Loading…
      </div>
    )
  }

  if (config.mode === 'clerk' && clerkConfigured) {
    return (
      <ClerkProvider
        publishableKey={clerkPubKey}
        proxyUrl={clerkProxyUrl}
        afterSignOutUrl="/"
        appearance={{
          variables: {
            colorPrimary: '#8c6bff',
            colorBackground: '#0c1122',
            colorForeground: '#f8fbff',
            colorMutedForeground: 'rgba(248, 251, 255, 0.66)',
            colorInput: 'rgba(255, 255, 255, 0.072)',
            colorInputForeground: '#f8fbff',
            borderRadius: '18px',
          },
        }}
      >
        <ClerkAuthBridge config={config}>{children}</ClerkAuthBridge>
      </ClerkProvider>
    )
  }

  return <LocalAuthBridge config={config}>{children}</LocalAuthBridge>
}
