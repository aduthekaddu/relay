// Authentication state. The shell's AuthGate loads it at boot; screens read
// `auth.value.user`. signOut() clears the server session and goes to /login.
import { signal } from '@preact/signals'
import { api } from '../api/client'
import type { AuthState } from '../api/types'

/** null while loading. */
export const auth = signal<AuthState | null>(null)
/** Set when the server could not be reached at boot. */
export const authError = signal<string | null>(null)

/** Fetch /api/v1/auth/state. */
export async function loadAuth(): Promise<AuthState | null> {
  try {
    auth.value = await api.get<AuthState>('auth/state')
    authError.value = null
  } catch (e) {
    authError.value = e instanceof Error ? e.message : 'unreachable'
  }
  return auth.value
}

/** End this session and show the sign-in screen. */
export async function signOut(): Promise<void> {
  try {
    await api.post('auth/logout')
  } catch {
    /* even if the call fails the cookie may be gone; continue */
  }
  auth.value = auth.value ? { ...auth.value, authenticated: false, user: undefined } : null
  location.assign('/login')
}
