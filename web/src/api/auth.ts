// Auth contract additions — mirrors internal/api/auth.go.
import type { AuthState, Passkey } from './types'

/** GET /api/v1/auth/state: AuthState plus first-run details. */
export interface AuthStateResponse extends AuthState {
  /** First-run setup from another device needs the one-time code printed by `relay serve`. */
  setupCodeRequired?: boolean
  /** WebAuthn is usable at this origin (HTTPS or localhost). */
  passkeysAvailable: boolean
}

/** POST /api/v1/auth/setup */
export interface SetupRequest {
  username: string
  password: string
  /** One-time setup code (see AuthStateResponse.setupCodeRequired). */
  code?: string
  remember?: boolean
}

/** {name}: passkey register/rename, token create. */
export interface NameRequest {
  name: string
}
/** {code}: TOTP enable/disable. */
export interface CodeRequest {
  code: string
}
/** GET /api/v1/auth/totp */
export interface TOTPStatus {
  enabled: boolean
}
/** POST /api/v1/auth/sessions/revoke-others */
export interface RevokedCount {
  revoked: number
}

/** Passkey with authenticator metadata (passkey list/register/rename). */
export interface PasskeyDetail extends Passkey {
  /** Backed up / synced (iCloud Keychain, Google Password Manager…). */
  synced: boolean
  transports?: string[]
  aaguid?: string
}
