// Root component: router, auth gate and the shell.
//
//   public routes (/login)  → rendered bare
//   everything else         → AuthGate → Shell → Router
//
// The gate loads /api/v1/auth/state once; signed-out visitors go to
// /login?next=<where they were>. Any API 401 afterwards (expired or
// revoked session) sends them to the same place.
import { useEffect } from 'preact/hooks'
import { LocationProvider, Route, Router, useLocation } from 'preact-iso'
import { onUnauthorized } from '../api/client'
import { loginUrl } from '../lib/url'
import { auth, authError, loadAuth } from '../state/auth'
import { startLive } from '../state/index'
import { NotFound, Unreachable } from './ErrorScreen'
import { routeCommitted } from './nav'
import { matchRoute, ROUTES } from './routes'
import { Shell } from './Shell'
import { routeLoadEnd, routeLoadStart } from './SignalLine'

function Routes() {
  return (
    <Router
      onLoadStart={routeLoadStart}
      onLoadEnd={routeLoadEnd}
      onRouteChange={() => {
        routeLoadEnd()
        routeCommitted()
      }}
    >
      {[
        ...ROUTES.map((r) => <Route key={r.path} path={r.path} component={r.component} />),
        <Route key="404" default component={NotFound} />,
      ]}
    </Router>
  )
}

/** Painted while auth state loads; matches the boot splash in index.html. */
function Splash() {
  return (
    <div class="boot" aria-busy="true" aria-label="Loading Relay">
      <svg viewBox="0 0 7 7" aria-hidden="true">
        {[
          [1, 1],
          [2, 1],
          [3, 1],
          [1, 2],
          [4, 2],
          [1, 3],
          [2, 3],
          [3, 3],
          [1, 4],
          [3, 4],
          [1, 5],
          [4, 5],
        ].map(([x, y]) => (
          <circle key={`${x}${y}`} cx={x + 0.5} cy={y + 0.5} r=".41" />
        ))}
        <circle class="b" cx="4.5" cy="1.5" r=".5" />
      </svg>
    </div>
  )
}

function Gate() {
  const loc = useLocation()
  const def = matchRoute(loc.path)
  const state = auth.value

  useEffect(() => {
    if (!auth.value) void loadAuth()
    return onUnauthorized(() => {
      if (matchRoute(location.pathname)?.public) return
      if (auth.value) auth.value = { ...auth.value, authenticated: false, user: undefined }
      loc.route(loginUrl(location.pathname + location.search + location.hash), true)
    })
  }, [])

  const needLogin = !!state && !state.authenticated && !def?.public
  useEffect(() => {
    if (needLogin) loc.route(loginUrl(loc.url), true)
  }, [needLogin])

  useEffect(() => {
    if (state?.authenticated) startLive()
  }, [state?.authenticated])

  if (def?.public) return <Routes />
  if (!state) return authError.value ? <Unreachable onRetry={loadAuth} /> : <Splash />
  if (!state.authenticated) return <Splash />
  return (
    <Shell route={def}>
      <Routes />
    </Shell>
  )
}

export function App() {
  return (
    <LocationProvider>
      <Gate />
    </LocationProvider>
  )
}
