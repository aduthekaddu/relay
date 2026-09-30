// App-wide state stores (Preact signals). See docs/dev/UI_KIT.md → "State".
import { connectEvents } from '../api/events'
import { trackConnection } from './connection'
import { loadInfo, trackInfo } from './info'
import { loadNotifications, trackNotifications } from './notifications'
import { loadTerminals, trackTerminals } from './terminals'

export { auth, authError, loadAuth, signOut } from './auth'
export { type Connection, type ConnectionState, connection, isOnline, retryConnection } from './connection'
export { info, loadInfo } from './info'
export {
  loadNotifications,
  markAllRead,
  markRead,
  notifications,
  notificationsLoaded,
  removeNotification,
  unreadCount,
} from './notifications'
export { loadTerminals, needsYou, removeTerminal, terminalList, terminals, terminalsLoaded, upsertTerminal, working } from './terminals'
export { setTheme, type Theme, type ThemePref, theme, themePref, toggleTheme } from './theme'

let started = false

/**
 * Start live state after sign-in: wire every store to the events socket,
 * load the initial lists and connect. Idempotent.
 */
export function startLive(): void {
  if (started) return
  started = true
  trackConnection()
  trackInfo()
  trackTerminals()
  trackNotifications()
  void loadInfo()
  void loadTerminals()
  void loadNotifications()
  connectEvents()
}
