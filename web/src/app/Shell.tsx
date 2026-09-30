// The app shell: desktop rail + header, mobile header + tab bar + More
// sheet, the signal line, immersive mode, notifications, palette, toasts,
// global shortcuts and view transitions.
import type { ComponentChildren } from 'preact'
import { lazy, Suspense } from 'preact/compat'
import { useEffect, useRef } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { setVisiblePath } from '../api/events'
import { closePalette, openPalette, palette, togglePalette } from '../command/state'
import { safeNext } from '../lib/url'
import { cx, isMac } from '../lib/util'
import { info } from '../state/info'
import { setNotificationNavigator } from '../state/notifications'
import { needsYou, working } from '../state/terminals'
import { Glyph, RelayMark } from '../ui/Glyph'
import { Icon } from '../ui/Icon'
import { Kbd } from '../ui/layout'
import { useMedia } from '../ui/overlay'
import { Toaster } from '../ui/Toast'
import { AREAS, type Area, type AreaId, areaById } from './areas'
import { immersiveOverride, pageChrome } from './chrome'
import { ErrorBoundary } from './ErrorScreen'
import { BellButton } from './Notifications'
import { interceptLinks, navigate, setRouter } from './nav'
import type { RouteDef } from './routes'
import { SignalLine } from './SignalLine'
import { ConnectionPill, DeviceDot, MoreSheet, moreOpen, ShortcutSheet, ThemeButton } from './Surfaces'
import { installShortcuts } from './shortcuts'
import './shell.css'

const Palette = lazy(() => import('../command/Palette'))
/** Warm the palette chunk when the browser is idle (instant first ⌘K). */
function preloadPalette() {
  const w = window as Window & { requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => number }
  const load = () => void import('../command/Palette')
  if (w.requestIdleCallback) w.requestIdleCallback(load, { timeout: 4000 })
  else window.setTimeout(load, 2500)
}

function useAreaCounts() {
  const list = needsYou.value
  let agents = 0
  let terms = 0
  for (const t of list) t.kind === 'agent' ? agents++ : terms++
  return { agents, terms, total: list.length }
}

function RailItem({ area, active, badge }: { area: Area; active: boolean; badge?: number }) {
  return (
    <a
      href={area.path}
      class={cx('rail-item', active && 'is-active')}
      style={{ '--hue': area.hue }}
      aria-current={active ? 'page' : undefined}
      title={`${area.label} · G ${area.goKey.toUpperCase()}`}
    >
      <Glyph
        name={area.id}
        size={20}
        state={active ? 'active' : 'idle'}
        color={active ? 'var(--hue)' : undefined}
      />
      <span class="rail-item__label">{area.label}</span>
      {badge ? (
        <span class="rail-item__badge" role="img" aria-label={`${badge} need you`}>
          {badge}
        </span>
      ) : null}
    </a>
  )
}

function Rail({ area, immersive }: { area: AreaId; immersive: boolean }) {
  const counts = useAreaCounts()
  const main = AREAS.filter((a) => a.id !== 'settings')
  return (
    <nav class="rail" aria-label="Areas">
      <a
        href="/"
        class="rail-logo"
        aria-label={counts.total ? `Relay home, ${counts.total} need you` : 'Relay home'}
      >
        <RelayMark size={26} beacon={counts.total > 0} />
      </a>
      <div class="rail-items">
        {main.map((a) => (
          <RailItem
            key={a.id}
            area={a}
            active={a.id === area}
            badge={a.id === 'agents' ? counts.agents : a.id === 'terminal' ? counts.terms : undefined}
          />
        ))}
      </div>
      <div class="rail-foot">
        {immersive && <BellButton />}
        <ThemeButton />
        <RailItem area={areaById('settings')} active={area === 'settings'} />
        <DeviceDot />
      </div>
    </nav>
  )
}

function SearchTrigger({ compact }: { compact: boolean }) {
  if (compact)
    return (
      <button type="button" class="hdr-icon" aria-label="Search and commands" onClick={() => openPalette()}>
        <Icon name="search" size={19} />
      </button>
    )
  return (
    <button
      type="button"
      class="search-trigger"
      onClick={() => openPalette()}
      aria-label="Search and commands"
      aria-keyshortcuts={isMac ? 'Meta+K' : 'Control+K'}
    >
      <Icon name="search" size={15} />
      <span class="search-trigger__text">Search or run a command</span>
      <Kbd keys={['mod', 'K']} />
    </button>
  )
}

function Header({ area, mobile }: { area: Area; mobile: boolean }) {
  const c = pageChrome.value
  const title = c.title ?? area.label
  return (
    <header class="hdr">
      <div class="hdr-title">
        {mobile && c.back ? (
          <a href={c.back} class="hdr-back" aria-label="Back">
            <Icon name="chevron-left" size={22} />
          </a>
        ) : mobile ? (
          <Glyph name={area.id} size={18} color="var(--hue)" class="hdr-glyph" />
        ) : null}
        <h1 class="hdr-h1">{title}</h1>
        {c.subtitle && <span class="hdr-sub">{c.subtitle}</span>}
      </div>
      <div class="hdr-actions">
        {c.actions}
        <ConnectionPill />
        <SearchTrigger compact={mobile} />
        <BellButton />
      </div>
    </header>
  )
}

function TabBar({ area }: { area: AreaId }) {
  const counts = useAreaCounts()
  const tabs = AREAS.filter((a) => a.mobileTab)
  const inMore = !tabs.some((t) => t.id === area)
  return (
    <nav class="tabbar" aria-label="Areas">
      {tabs.map((a) => {
        const badge = a.id === 'agents' ? counts.agents : a.id === 'terminal' ? counts.terms : 0
        const active = a.id === area
        return (
          <a
            key={a.id}
            href={a.path}
            class={cx('tab', active && 'is-active')}
            style={{ '--hue': a.hue }}
            aria-current={active ? 'page' : undefined}
          >
            <span class="tab__glyph">
              <Glyph
                name={a.id}
                size={22}
                state={active ? 'active' : 'idle'}
                color={active ? 'var(--hue)' : undefined}
              />
              {badge > 0 && <span class="tab__badge" role="img" aria-label={`${badge} need you`} />}
            </span>
            <span class="tab__label">{a.label}</span>
          </a>
        )
      })}
      <button
        type="button"
        class={cx('tab', inMore && 'is-active')}
        style={inMore ? { '--hue': areaById(area).hue } : undefined}
        aria-haspopup="dialog"
        aria-expanded={moreOpen.value}
        onClick={() => (moreOpen.value = true)}
      >
        <span class="tab__glyph">
          <Icon name="ellipsis" size={22} />
        </span>
        <span class="tab__label">More</span>
      </button>
    </nav>
  )
}

export interface ShellProps {
  route: RouteDef | null
  children: ComponentChildren
}

/** Everything around the current screen. */
export function Shell({ route, children }: ShellProps) {
  const loc = useLocation()
  const mobile = useMedia('(max-width: 767px)')
  const area = areaById(route?.area ?? 'home')
  const immersive = immersiveOverride.value ?? !!route?.immersive
  const main = useRef<HTMLElement>(null)
  const inTerminal = area.id === 'terminal'
  const liveWork = inTerminal && working.value.length > 0

  // Router hooks for programmatic navigation + notifications deep links.
  useEffect(() => {
    setRouter(loc.route)
    setNotificationNavigator((url) => navigate(url))
  }, [loc.route])
  useEffect(() => interceptLinks(), [])
  // Notification clicks from the service worker route this tab.
  useEffect(() => {
    const sw = navigator.serviceWorker
    if (!sw) return
    const onMsg = (e: MessageEvent) => {
      const d = e.data as { type?: string; url?: string } | null
      if (d?.type === 'relay:navigate' && typeof d.url === 'string') navigate(safeNext(d.url))
    }
    sw.addEventListener('message', onMsg)
    return () => sw.removeEventListener('message', onMsg)
  }, [])
  useEffect(() => {
    const off = installShortcuts(
      {
        togglePalette,
        openPalette: () => openPalette(),
        navigate: (p) => navigate(p),
        modalOpen: () => document.documentElement.classList.contains('has-modal'),
      },
      isMac,
    )
    preloadPalette()
    return off
  }, [])

  // Tell the server what is on screen (notification suppression).
  useEffect(() => {
    setVisiblePath(loc.path)
    closePalette()
    moreOpen.value = false
  }, [loc.url])

  // Document title: "<screen> · <host>".
  const title = pageChrome.value.title ?? area.label
  const host = info.value?.hostname
  useEffect(() => {
    const unread = needsYou.value.length
    document.title = `${unread ? `(${unread}) ` : ''}${title}${host ? ` · ${host}` : ''} — Relay`
  }, [title, host, needsYou.value.length])

  return (
    <div
      class={cx('shell', immersive && 'shell--immersive', mobile ? 'shell--mobile' : 'shell--desktop')}
      data-area={area.id}
      style={{ '--hue': area.hue }}
    >
      <a class="skip-link" href="#main">
        Skip to content
      </a>
      <SignalLine working={liveWork} />
      {!mobile && <Rail area={area.id} immersive={immersive} />}
      <div class="shell-main">
        {!immersive && <Header area={area} mobile={mobile} />}
        <main id="main" ref={main} class={cx('content', immersive && 'content--bleed')} tabIndex={-1}>
          <ErrorBoundary resetKey={loc.path}>{children}</ErrorBoundary>
        </main>
      </div>
      {mobile && !immersive && <TabBar area={area.id} />}
      {mobile && <MoreSheet />}
      <ShortcutSheet />
      {palette.value && (
        <Suspense fallback={null}>
          <Palette />
        </Suspense>
      )}
      <Toaster />
    </div>
  )
}
