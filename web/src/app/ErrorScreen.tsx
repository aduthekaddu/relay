// Friendly failure screens: the per-screen error boundary, not-found, and
// "couldn't reach the machine" (boot without a server).
import { Component, type ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { Button } from '../ui/Button'
import { EmptyState } from '../ui/layout'

interface BoundaryProps {
  /** Changing this (e.g. the URL) clears the error. */
  resetKey: string
  children: ComponentChildren
}

interface BoundaryState {
  error: Error | null
  key: string
}

/** Catches render errors in the current screen and offers a way out. */
export class ErrorBoundary extends Component<BoundaryProps, BoundaryState> {
  override state: BoundaryState = { error: null, key: this.props.resetKey }

  static override getDerivedStateFromProps(
    props: BoundaryProps,
    state: BoundaryState,
  ): Partial<BoundaryState> | null {
    return props.resetKey !== state.key ? { error: null, key: props.resetKey } : null
  }

  override componentDidCatch(error: Error) {
    // Lazy-route promises are handled by preact-iso; only real errors land here.
    if (error && typeof (error as unknown as PromiseLike<unknown>).then === 'function') throw error
    console.error('[relay] screen crashed', error)
    this.setState({ error })
  }

  override render() {
    if (this.state.error)
      return <ErrorScreen error={this.state.error} onRetry={() => this.setState({ error: null })} />
    return this.props.children
  }
}

export function ErrorScreen({ error, onRetry }: { error: Error; onRetry: () => void }) {
  const [details, setDetails] = useState(false)
  const chunk = /dynamically imported module|Failed to fetch|Importing a module script failed/i.test(
    error.message,
  )
  return (
    <div class="screen-center" role="alert">
      <EmptyState
        glyph="alert"
        hue="var(--warn)"
        title={chunk ? 'Relay was updated' : 'This screen hit a problem'}
        body={
          chunk
            ? 'A newer version is on the machine. Reload to get it.'
            : 'Nothing on the machine was affected. Try again, or go back home.'
        }
        action={
          <div class="row" style={{ '--gap': 'var(--s-2)', justifyContent: 'center' }}>
            {chunk ? (
              <Button variant="primary" icon="refresh-cw" onClick={() => location.reload()}>
                Reload
              </Button>
            ) : (
              <>
                <Button variant="primary" onClick={onRetry}>
                  Try again
                </Button>
                <Button variant="ghost" href="/">
                  Go home
                </Button>
              </>
            )}
          </div>
        }
      />
      {!chunk && (
        <div class="error-details">
          <button
            type="button"
            class="link-quiet"
            aria-expanded={details}
            onClick={() => setDetails((d) => !d)}
          >
            {details ? 'Hide details' : 'Show details'}
          </button>
          {details && (
            <pre class="error-details__pre">{`${error.name}: ${error.message}\n${(error.stack ?? '').split('\n').slice(1, 6).join('\n')}`}</pre>
          )}
        </div>
      )}
    </div>
  )
}

export function NotFound() {
  return (
    <div class="screen-center">
      <EmptyState
        glyph="empty"
        title="Nothing here"
        body="This address doesn’t match a screen in Relay."
        action={
          <Button variant="primary" href="/">
            Go home
          </Button>
        }
      />
    </div>
  )
}

/** Boot screen when /auth/state cannot be reached; retries with backoff. */
export function Unreachable({ onRetry }: { onRetry: () => Promise<unknown> }) {
  const [attempt, setAttempt] = useState(0)
  const [left, setLeft] = useState(2)
  useEffect(() => {
    const wait = Math.min(30, 2 ** Math.min(attempt, 4) + 1)
    setLeft(wait)
    const tick = window.setInterval(() => setLeft((s) => Math.max(0, s - 1)), 1000)
    const t = window.setTimeout(() => {
      void onRetry().finally(() => setAttempt((a) => a + 1))
    }, wait * 1000)
    return () => {
      window.clearInterval(tick)
      window.clearTimeout(t)
    }
  }, [attempt])
  return (
    <div class="screen-center screen-center--full grain" role="alert">
      <EmptyState
        glyph="relay"
        hue="var(--signal)"
        title="Couldn’t reach the machine"
        body={
          left > 0
            ? `Retrying in ${left}s. Check that Relay is running and this device is online.`
            : 'Retrying…'
        }
        action={
          <Button
            variant="secondary"
            icon="refresh-cw"
            onClick={() => void onRetry().finally(() => setAttempt((a) => a + 1))}
          >
            Retry now
          </Button>
        }
      />
    </div>
  )
}
