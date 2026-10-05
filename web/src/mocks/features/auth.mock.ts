// Synthetic auth fixtures; owned registrations and scenario controls.
import type { LoginRequest } from '../../api/types'
import * as db from '../data'
import { body, now, qn } from '../helpers'
import { defineMockModule, type MockOwner } from '../registry'
import { fail, modes, newId, noContent, notFound, ok, setModes } from '../util'

export default defineMockModule('auth', (owner) => {
  const publicPaths = new Set([
    '/auth/state',
    '/auth/setup',
    '/auth/login',
    '/auth/passkey/begin',
    '/auth/passkey/finish',
  ])
  const route: MockOwner['http'] = (method, path, handler, options) =>
    owner.http(method, path, handler, {
      auth: publicPaths.has(path) ? 'public' : 'authenticated',
      ...options,
    })
  /** Signing in clears the signed-out scenarios so a reload stays signed in. */
  function signedIn(user: string): void {
    db.auth.state = { ...db.auth.state, authenticated: true, setupRequired: false, user }
    setModes([...modes].filter((m) => m !== 'signed-out' && m !== 'setup' && m !== 'totp'))
  }

  route('GET', '/auth/state', () => ok(db.auth.state))
  route('POST', '/auth/setup', (r) => {
    const b = body<{ username?: string; password?: string }>(r)
    if (!db.auth.state.setupRequired) return fail(409, 'already_setup', 'An account already exists.')
    if (!b.username?.trim()) return fail(400, 'invalid', 'Choose a username.', { field: 'username' })
    if ((b.password ?? '').length < 10)
      return fail(400, 'weak_password', 'Use at least 10 characters.', { field: 'password' })
    signedIn(b.username.trim())
    return ok({ ok: true, setupPasskey: true })
  })
  route('POST', '/auth/login', (r) => {
    const b = body<LoginRequest>(r)
    if (Date.now() < db.auth.lockedUntil) {
      const retryIn = Math.ceil((db.auth.lockedUntil - Date.now()) / 1000)
      return fail(429, 'rate_limited', 'Too many attempts. Try again soon.', { retryIn })
    }
    // Any username works; the password "wrong" (or empty) fails.
    if (!b.password || b.password === 'wrong') {
      db.auth.failures++
      if (db.auth.failures >= 3) {
        db.auth.lockedUntil = Date.now() + 30_000
        db.auth.failures = 0
        return fail(429, 'rate_limited', 'Too many attempts. Try again soon.', { retryIn: 30 })
      }
      return fail(401, 'bad_credentials', 'That username and password don’t match.')
    }
    if (db.auth.state.methods.totp) {
      if (!b.totp) return ok({ ok: false, needTotp: true })
      if (b.totp !== '123456')
        return fail(401, 'bad_totp', 'That code didn’t work. Codes change every 30 seconds.', {
          field: 'totp',
        })
    }
    db.auth.failures = 0
    signedIn(b.username || 'dev')
    return ok({ ok: true })
  })
  route('POST', '/auth/logout', () => {
    db.auth.state = { ...db.auth.state, authenticated: false, user: undefined }
    setModes([...modes.add('signed-out')])
    return noContent()
  })
  route('POST', '/auth/passkey/begin', () =>
    ok({
      challenge: 'bW9jay1jaGFsbGVuZ2UtZm9yLWRldmVsb3BtZW50',
      rpId: location.hostname,
      timeout: 60000,
      userVerification: 'preferred',
      allowCredentials: [],
    }),
  )
  route('POST', '/auth/passkey/finish', () => {
    signedIn('dev')
    return ok({ ok: true })
  })
  route('GET', '/auth/passkeys', () => ok(db.passkeys))
  route('POST', '/auth/passkeys/begin', (r) =>
    ok({
      challenge: 'bW9jay1jaGFsbGVuZ2U',
      rp: { id: location.hostname, name: 'Relay' },
      user: { id: 'ZGV2', name: 'dev', displayName: body<{ name?: string }>(r).name ?? 'dev' },
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      authenticatorSelection: { residentKey: 'required', userVerification: 'preferred' },
      timeout: 60000,
    }),
  )
  route('POST', '/auth/passkeys/finish', () => {
    const pk = { id: newId('pk'), name: 'New passkey', createdAt: now() }
    db.passkeys.push(pk)
    return ok(pk)
  })
  route('PATCH', '/auth/passkeys/{id}', (r) => {
    const pk = db.passkeys.find((p) => p.id === r.params.id)
    if (!pk) return notFound('Passkey not found')
    pk.name = body<{ name: string }>(r).name ?? pk.name
    return ok(pk)
  })
  route('DELETE', '/auth/passkeys/{id}', (r) => {
    const i = db.passkeys.findIndex((p) => p.id === r.params.id)
    if (i >= 0) db.passkeys.splice(i, 1)
    return noContent()
  })
  route('POST', '/auth/password', (r) => {
    const b = body<{ current?: string; next?: string }>(r)
    if (b.current === 'wrong')
      return fail(401, 'bad_credentials', 'Your current password is incorrect.', { field: 'current' })
    if ((b.next ?? '').length < 10)
      return fail(400, 'weak_password', 'Use at least 10 characters.', { field: 'next' })
    return noContent()
  })
  let totpEnabled = db.auth.state.methods.totp
  route('GET', '/auth/totp', () => ok({ enabled: totpEnabled }))
  route('POST', '/auth/totp/setup', () =>
    ok({
      secret: 'JBSWY3DPEHPK3PXP',
      otpauthUrl: 'otpauth://totp/Relay:dev?secret=JBSWY3DPEHPK3PXP&issuer=Relay',
    }),
  )
  route('POST', '/auth/totp/enable', (r) => {
    if (body<{ code?: string }>(r).code !== '123456')
      return fail(400, 'bad_totp', 'That code didn’t work.', { field: 'code' })
    totpEnabled = true
    return noContent()
  })
  route('POST', '/auth/totp/disable', () => {
    totpEnabled = false
    return noContent()
  })
  route('GET', '/auth/sessions', () => ok(db.deviceSessions))
  route('DELETE', '/auth/sessions/{id}', (r) => {
    const i = db.deviceSessions.findIndex((s) => s.id === r.params.id && !s.current)
    if (i >= 0) db.deviceSessions.splice(i, 1)
    return noContent()
  })
  route('POST', '/auth/sessions/revoke-others', () => {
    const n = db.deviceSessions.filter((s) => !s.current).length
    db.deviceSessions.splice(0, db.deviceSessions.length, ...db.deviceSessions.filter((s) => s.current))
    return ok({ revoked: n })
  })
  route('GET', '/auth/tokens', () => ok(db.tokens))
  route('POST', '/auth/tokens', (r) => {
    const tk = {
      id: newId('tk'),
      name: body<{ name?: string }>(r).name || 'Untitled token',
      prefix: 'rly_9z1x',
      createdAt: now(),
    }
    db.tokens.push(tk)
    return ok({ ...tk, token: 'rly_9z1x_mock_token_value_shown_once_0000000000' })
  })
  route('DELETE', '/auth/tokens/{id}', (r) => {
    const i = db.tokens.findIndex((t) => t.id === r.params.id)
    if (i >= 0) db.tokens.splice(i, 1)
    return noContent()
  })
  route('GET', '/auth/activity', (r) => ok(db.audit.slice(0, qn(r, 'limit', 50))))
  owner.onReset(() => {
    totpEnabled = db.auth.state.methods.totp
  })
})
