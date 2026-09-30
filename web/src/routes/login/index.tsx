// Sign-in screen: password (+ optional TOTP step), passkeys (button and
// conditional autofill), and first-run "Create your account". Keeps the
// ?next= target (sanitised) and goes there afterwards.
import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { ApiError, api } from '../../api/client'
import type { AuthState, LoginResponse } from '../../api/types'
import { passwordStrength } from '../../lib/strength'
import { safeNext } from '../../lib/url'
import {
  assertionToJSON,
  conditionalMediationAvailable,
  getAssertion,
  isUserCancel,
  parseRequestOptions,
  passkeysSupported,
  type RequestOptionsJSON,
} from '../../lib/webauthn'
import { auth, loadAuth } from '../../state/auth'
import { Button } from '../../ui/Button'
import { DotText } from '../../ui/DotText'
import { Checkbox, Field, Input } from '../../ui/form'
import { Icon } from '../../ui/Icon'
import { DotField } from './DotField'
import './login.css'

type Mode = 'signin' | 'totp' | 'setup'

function useCountdown(): [number, (sec: number) => void] {
  const [left, setLeft] = useState(0)
  useEffect(() => {
    if (left <= 0) return
    const t = window.setTimeout(() => setLeft((s) => s - 1), 1000)
    return () => window.clearTimeout(t)
  }, [left])
  return [left, setLeft]
}

function messageFor(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 401) return e.message || 'That username and password don’t match.'
    if (e.status >= 500) return 'The machine had a problem signing you in. Try again.'
    return e.message
  }
  return 'Couldn’t reach the machine. Check the connection and try again.'
}

export default function LoginRoute() {
  const loc = useLocation()
  const next = safeNext(loc.query.next)
  const state = auth.value
  const [mode, setMode] = useState<Mode>('signin')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [totp, setTotp] = useState('')
  const [remember, setRemember] = useState(true)
  const [show, setShow] = useState(false)
  const [busy, setBusy] = useState<'password' | 'passkey' | null>(null)
  const [error, setError] = useState<{ text: string; field?: string } | null>(null)
  const [locked, lockFor] = useCountdown()
  const conditional = useRef<AbortController | null>(null)
  const userRef = useRef<HTMLInputElement>(null)
  const totpRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!auth.value) void loadAuth()
  }, [])
  useEffect(() => {
    if (state?.setupRequired) setMode('setup')
  }, [state?.setupRequired])

  const done = async () => {
    conditional.current?.abort()
    const s = await loadAuth()
    if (s?.authenticated) loc.route(next, true)
  }

  // Already signed in (e.g. opened /login in a second tab).
  useEffect(() => {
    if (state?.authenticated) loc.route(next, true)
  }, [state?.authenticated])

  const passkeyOK = passkeysSupported() && (state?.methods.passkey ?? false) && mode === 'signin'

  // Conditional mediation: passkeys offered right in the username autofill.
  useEffect(() => {
    if (!passkeyOK) return
    let live = true
    void (async () => {
      if (!(await conditionalMediationAvailable()) || !live) return
      const ctrl = new AbortController()
      conditional.current = ctrl
      try {
        const opts = await api.post<RequestOptionsJSON>('auth/passkey/begin', undefined, {
          signal: ctrl.signal,
        })
        const cred = await getAssertion(parseRequestOptions(opts), { conditional: true, signal: ctrl.signal })
        if (!cred || !live) return
        setBusy('passkey')
        await api.post<LoginResponse>('auth/passkey/finish', assertionToJSON(cred))
        await done()
      } catch (e) {
        if (live && !isUserCancel(e) && !(e instanceof DOMException)) setError({ text: messageFor(e) })
      } finally {
        if (live) setBusy(null)
      }
    })()
    return () => {
      live = false
      conditional.current?.abort()
    }
  }, [passkeyOK])

  const fail = (e: unknown) => {
    if (e instanceof ApiError && e.status === 429) {
      lockFor(Math.max(1, Math.ceil(e.retryIn ?? 30)))
      setError({ text: 'Too many attempts.' })
      return
    }
    setError({ text: messageFor(e), field: e instanceof ApiError ? e.field : undefined })
  }

  const signIn = async (e: Event) => {
    e.preventDefault()
    if (busy || locked) return
    if (!username.trim() || !password) {
      setError({
        text: !username.trim() ? 'Enter your username.' : 'Enter your password.',
        field: !username.trim() ? 'username' : 'password',
      })
      return
    }
    setBusy('password')
    setError(null)
    try {
      const res = await api.post<LoginResponse>('auth/login', {
        username: username.trim(),
        password,
        remember,
        ...(mode === 'totp' ? { totp } : {}),
      })
      if (res.needTotp) {
        setMode('totp')
        requestAnimationFrame(() => totpRef.current?.focus())
        return
      }
      await done()
    } catch (err) {
      fail(err)
      if (mode === 'totp') {
        setTotp('')
        requestAnimationFrame(() => totpRef.current?.focus())
      }
    } finally {
      setBusy(null)
    }
  }

  const passkey = async () => {
    conditional.current?.abort()
    setBusy('passkey')
    setError(null)
    try {
      const opts = await api.post<RequestOptionsJSON>('auth/passkey/begin')
      const cred = await getAssertion(parseRequestOptions(opts))
      if (!cred) return
      await api.post<LoginResponse>('auth/passkey/finish', assertionToJSON(cred))
      await done()
    } catch (err) {
      if (!isUserCancel(err)) fail(err)
    } finally {
      setBusy(null)
    }
  }

  const strength = useMemo(
    () => passwordStrength(password, [username, state?.user ?? '']),
    [password, username],
  )
  const setup = async (e: Event) => {
    e.preventDefault()
    if (busy) return
    if (!username.trim()) return setError({ text: 'Choose a username.', field: 'username' })
    if (strength.score < 2)
      return setError({ text: strength.hint || 'Choose a stronger password.', field: 'password' })
    if (confirm !== password) return setError({ text: 'The passwords don’t match.', field: 'confirm' })
    setBusy('password')
    setError(null)
    try {
      await api.post<LoginResponse>('auth/setup', { username: username.trim(), password })
      await done()
    } catch (err) {
      fail(err)
    } finally {
      setBusy(null)
    }
  }

  // Auto-submit a complete TOTP code.
  useEffect(() => {
    if (mode === 'totp' && totp.length === 6 && !busy) void signIn(new Event('submit'))
  }, [totp])

  const err = (f: string) => (error?.field === f ? error.text : undefined)
  const formError = error && !error.field ? error.text : null

  return (
    <div class="login">
      <DotField />
      <main class="login-col">
        <header class="login-brand">
          <DotText text="RELAY" pitch={7} reveal class="login-word" />
          <p class="login-host">{hostLine(state)}</p>
        </header>

        <section class="login-card" aria-labelledby="login-title">
          {mode === 'setup' ? (
            <form onSubmit={setup} noValidate>
              <h1 id="login-title" class="login-title">
                Create your account
              </h1>
              <p class="login-lede">This machine has no account yet. The first one becomes its owner.</p>
              <div class="login-fields">
                <Field label="Username" error={err('username')}>
                  <Input
                    size="lg"
                    autoComplete="username"
                    autoCapitalize="off"
                    spellcheck={false}
                    value={username}
                    onInput={(e) => setUsername(e.currentTarget.value)}
                    autoFocus
                  />
                </Field>
                <Field label="Password" error={err('password')}>
                  <Input
                    size="lg"
                    type={show ? 'text' : 'password'}
                    autoComplete="new-password"
                    value={password}
                    onInput={(e) => setPassword(e.currentTarget.value)}
                    trailing={<ShowToggle show={show} onToggle={() => setShow((s) => !s)} />}
                  />
                </Field>
                <StrengthMeter
                  score={password ? strength.score : -1}
                  label={password ? strength.label : ''}
                  hint={password ? strength.hint : 'Long passphrases beat clever symbols.'}
                />
                <Field label="Confirm password" error={err('confirm')}>
                  <Input
                    size="lg"
                    type={show ? 'text' : 'password'}
                    autoComplete="new-password"
                    value={confirm}
                    onInput={(e) => setConfirm(e.currentTarget.value)}
                  />
                </Field>
              </div>
              {formError && <FormError text={formError} />}
              <Button
                type="submit"
                variant="primary"
                size="lg"
                class="login-submit"
                loading={busy === 'password'}
              >
                Create account
              </Button>
              <p class="login-fine">You can add a passkey and two-step codes in Settings → Security.</p>
            </form>
          ) : mode === 'totp' ? (
            <form onSubmit={signIn} noValidate>
              <h1 id="login-title" class="login-title">
                Two-step code
              </h1>
              <p class="login-lede">Enter the 6-digit code from your authenticator app.</p>
              <div class="login-fields">
                <Field
                  label="Code"
                  hideLabel
                  error={err('totp') ?? (error && !error.field && !locked ? error.text : undefined)}
                >
                  <Input
                    ref={totpRef}
                    size="lg"
                    mono
                    class="login-totp"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    pattern="[0-9]*"
                    maxLength={6}
                    placeholder="000000"
                    value={totp}
                    onInput={(e) => setTotp(e.currentTarget.value.replace(/\D/g, '').slice(0, 6))}
                  />
                </Field>
              </div>
              {locked > 0 && <FormError text={`Too many attempts. Try again in ${locked}s.`} />}
              <Button
                type="submit"
                variant="primary"
                size="lg"
                class="login-submit"
                loading={busy === 'password'}
                disabled={totp.length !== 6 || locked > 0}
              >
                Verify
              </Button>
              <button
                type="button"
                class="login-link"
                onClick={() => {
                  setMode('signin')
                  setTotp('')
                  setError(null)
                }}
              >
                <Icon name="chevron-left" size={14} /> Use a different account
              </button>
            </form>
          ) : (
            <form onSubmit={signIn} noValidate>
              <h1 id="login-title" class="login-title">
                Sign in
              </h1>
              <div class="login-fields">
                <Field label="Username" error={err('username')}>
                  <Input
                    ref={userRef}
                    size="lg"
                    name="username"
                    autoComplete="username webauthn"
                    autoCapitalize="off"
                    autoCorrect="off"
                    spellcheck={false}
                    value={username}
                    onInput={(e) => setUsername(e.currentTarget.value)}
                    autoFocus
                  />
                </Field>
                <Field label="Password" error={err('password')}>
                  <Input
                    size="lg"
                    name="password"
                    type={show ? 'text' : 'password'}
                    autoComplete="current-password"
                    value={password}
                    onInput={(e) => setPassword(e.currentTarget.value)}
                    trailing={<ShowToggle show={show} onToggle={() => setShow((s) => !s)} />}
                  />
                </Field>
                <Checkbox checked={remember} onChange={setRemember} label="Keep me signed in for 30 days" />
              </div>
              {(formError || locked > 0) && (
                <FormError
                  text={locked > 0 ? `Too many attempts. Try again in ${locked}s.` : (formError ?? '')}
                />
              )}
              <Button
                type="submit"
                variant="primary"
                size="lg"
                class="login-submit"
                loading={busy === 'password'}
                disabled={locked > 0}
              >
                {locked > 0 ? `Wait ${locked}s` : 'Sign in'}
              </Button>
              {passkeyOK && (
                <>
                  <div class="login-or" aria-hidden="true">
                    <span>or</span>
                  </div>
                  <Button
                    type="button"
                    variant="secondary"
                    size="lg"
                    icon="key-round"
                    class="login-submit"
                    loading={busy === 'passkey'}
                    disabled={locked > 0}
                    onClick={() => void passkey()}
                  >
                    Sign in with a passkey
                  </Button>
                </>
              )}
            </form>
          )}
        </section>
        <p class="login-foot">Private workspace. Access is logged.</p>
      </main>
    </div>
  )
}

function hostLine(s: AuthState | null): string {
  const host = typeof location !== 'undefined' ? location.hostname : ''
  if (s?.setupRequired) return `${host} · first run`
  return host
}

function ShowToggle({ show, onToggle }: { show: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      class="login-eye"
      aria-label={show ? 'Hide password' : 'Show password'}
      aria-pressed={show}
      onClick={onToggle}
    >
      <Icon name={show ? 'eye-off' : 'eye'} size={16} />
    </button>
  )
}

function FormError({ text }: { text: string }) {
  return (
    <p class="login-error" role="alert">
      <Icon name="circle-alert" size={15} />
      <span>{text}</span>
    </p>
  )
}

/** Four dots that light up with the password's strength (0–4). */
export function StrengthMeter({ score, label, hint }: { score: number; label: string; hint: string }) {
  const tone = score >= 3 ? 'ok' : score === 2 ? 'warn' : 'danger'
  return (
    <div class="strength" aria-live="polite">
      <span class={`strength__dots strength--${tone}`} aria-hidden="true">
        {[1, 2, 3, 4].map((i) => (
          <i key={i} class={score >= i ? 'on' : ''} />
        ))}
      </span>
      <span class="strength__text">
        {label && <strong>{label}</strong>}
        {hint && <span>{hint}</span>}
      </span>
    </div>
  )
}
