// STUB shell — replaced by the web-shell feature (rail, tab bar, header,
// command center, toasts, auth gate).
import { ErrorBoundary, LocationProvider, Route, Router } from 'preact-iso'
import { ROUTES } from './routes'

export function App() {
  return (
    <LocationProvider>
      <ErrorBoundary>
        <Router>
          {ROUTES.map((r) => (
            <Route key={r.path} path={r.path} component={r.component} />
          ))}
        </Router>
      </ErrorBoundary>
    </LocationProvider>
  )
}
