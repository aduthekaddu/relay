// WebAuthn helpers: base64url <-> ArrayBuffer and JSON (de)serialisation
// of credential options/results, so the server can speak plain JSON
// (the go-webauthn wire format) and the browser gets real BufferSources.

/** Encode bytes as unpadded base64url. */
export function toB64u(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf) : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** Decode base64url (padded or not) to an ArrayBuffer. Throws on bad input. */
export function fromB64u(s: string): ArrayBuffer {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/')
  const pad = b64.length % 4 === 0 ? '' : '='.repeat(4 - (b64.length % 4))
  const bin = atob(b64 + pad)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out.buffer
}

interface JsonDescriptor {
  id: string
  type: string
  transports?: string[]
}

/** Server JSON for navigator.credentials.get (optionally wrapped in {publicKey}). */
export interface RequestOptionsJSON {
  challenge: string
  timeout?: number
  rpId?: string
  allowCredentials?: JsonDescriptor[]
  userVerification?: UserVerificationRequirement
  extensions?: Record<string, unknown>
}

/** Server JSON for navigator.credentials.create (optionally wrapped in {publicKey}). */
export interface CreationOptionsJSON {
  challenge: string
  rp: { id?: string; name: string }
  user: { id: string; name: string; displayName: string }
  pubKeyCredParams: { type: 'public-key'; alg: number }[]
  timeout?: number
  excludeCredentials?: JsonDescriptor[]
  authenticatorSelection?: AuthenticatorSelectionCriteria
  attestation?: AttestationConveyancePreference
  extensions?: Record<string, unknown>
}

const unwrap = <T>(o: T | { publicKey: T }): T =>
  o && typeof o === 'object' && 'publicKey' in (o as object) ? (o as { publicKey: T }).publicKey : (o as T)

const descriptors = (list?: JsonDescriptor[]): PublicKeyCredentialDescriptor[] | undefined =>
  list?.map((d) => ({ id: fromB64u(d.id), type: 'public-key', transports: d.transports as AuthenticatorTransport[] }))

/** Convert server request options to the browser shape. */
export function parseRequestOptions(json: RequestOptionsJSON | { publicKey: RequestOptionsJSON }): PublicKeyCredentialRequestOptions {
  const o = unwrap(json)
  return {
    challenge: fromB64u(o.challenge),
    timeout: o.timeout,
    rpId: o.rpId,
    allowCredentials: descriptors(o.allowCredentials),
    userVerification: o.userVerification,
    extensions: o.extensions as AuthenticationExtensionsClientInputs | undefined,
  }
}

/** Convert server creation options to the browser shape. */
export function parseCreationOptions(
  json: CreationOptionsJSON | { publicKey: CreationOptionsJSON },
): PublicKeyCredentialCreationOptions {
  const o = unwrap(json)
  return {
    challenge: fromB64u(o.challenge),
    rp: o.rp,
    user: { ...o.user, id: fromB64u(o.user.id) },
    pubKeyCredParams: o.pubKeyCredParams,
    timeout: o.timeout,
    excludeCredentials: descriptors(o.excludeCredentials),
    authenticatorSelection: o.authenticatorSelection,
    attestation: o.attestation,
    extensions: o.extensions as AuthenticationExtensionsClientInputs | undefined,
  }
}

/** JSON body for /auth/passkey/finish. */
export function assertionToJSON(cred: PublicKeyCredential): Record<string, unknown> {
  const r = cred.response as AuthenticatorAssertionResponse
  return {
    id: cred.id,
    rawId: toB64u(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      clientDataJSON: toB64u(r.clientDataJSON),
      authenticatorData: toB64u(r.authenticatorData),
      signature: toB64u(r.signature),
      userHandle: r.userHandle ? toB64u(r.userHandle) : undefined,
    },
  }
}

/** JSON body for /auth/passkeys/finish (registration). */
export function attestationToJSON(cred: PublicKeyCredential): Record<string, unknown> {
  const r = cred.response as AuthenticatorAttestationResponse
  return {
    id: cred.id,
    rawId: toB64u(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      clientDataJSON: toB64u(r.clientDataJSON),
      attestationObject: toB64u(r.attestationObject),
      transports: typeof r.getTransports === 'function' ? r.getTransports() : undefined,
    },
  }
}

/** True when this browser can use passkeys at all. */
export function passkeysSupported(): boolean {
  return typeof window !== 'undefined' && typeof window.PublicKeyCredential === 'function' && !!navigator.credentials
}

/** True when the browser supports conditional mediation (passkey autofill). */
export async function conditionalMediationAvailable(): Promise<boolean> {
  if (!passkeysSupported()) return false
  const fn = (PublicKeyCredential as unknown as { isConditionalMediationAvailable?: () => Promise<boolean> })
    .isConditionalMediationAvailable
  try {
    return typeof fn === 'function' ? await fn.call(PublicKeyCredential) : false
  } catch {
    return false
  }
}

/**
 * Run a WebAuthn assertion. `conditional` uses autofill UI (resolves only if
 * the user picks a passkey from the username field's suggestions).
 */
export async function getAssertion(
  options: PublicKeyCredentialRequestOptions,
  opts: { conditional?: boolean; signal?: AbortSignal } = {},
): Promise<PublicKeyCredential | null> {
  const cred = await navigator.credentials.get({
    publicKey: options,
    signal: opts.signal,
    mediation: opts.conditional ? ('conditional' as CredentialMediationRequirement) : undefined,
  })
  return (cred as PublicKeyCredential | null) ?? null
}

/** Register a new passkey. */
export async function createCredential(
  options: PublicKeyCredentialCreationOptions,
  signal?: AbortSignal,
): Promise<PublicKeyCredential | null> {
  const cred = await navigator.credentials.create({ publicKey: options, signal })
  return (cred as PublicKeyCredential | null) ?? null
}

/** True when a WebAuthn error means "the user dismissed the prompt". */
export function isUserCancel(err: unknown): boolean {
  return err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'AbortError')
}
