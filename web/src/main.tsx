import { render } from 'preact'
import { App } from './app/App'
import { trackViewport } from './app/viewport'
import { registerAll } from './command/all'
import { trackTheme } from './state/theme'
import './styles/index.css'

async function boot() {
  // Dev-only mock backend (VITE_MOCK=1). Statically false in production
  // builds, so the mocks are tree-shaken out of the bundle.
  if (import.meta.env.DEV && import.meta.env.VITE_MOCK === '1') {
    const { installMocks } = await import('./mocks')
    installMocks()
  }
  trackTheme()
  trackViewport()
  registerAll()
  render(<App />, document.getElementById('app')!)

  if (import.meta.env.PROD && 'serviceWorker' in navigator && window.isSecureContext) {
    const register = () =>
      navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((err) => console.warn('[relay] service worker', err))
    if (document.readyState === 'complete') void register()
    else window.addEventListener('load', () => void register(), { once: true })
  }
}

void boot()
