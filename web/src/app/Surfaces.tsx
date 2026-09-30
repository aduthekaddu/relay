// Secondary shell surfaces: the mobile "More" sheet, the keyboard
// shortcut sheet, the theme picker and the connection indicator.
import { signal } from '@preact/signals'
import { useEffect, useRef, useState } from 'preact/hooks'
import { cx, isMac } from '../lib/util'
import { signOut } from '../state/auth'
import { connection, retryConnection } from '../state/connection'
import { info } from '../state/info'
import { setTheme, type ThemePref, themePref } from '../state/theme'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Segmented } from '../ui/form'
import { Glyph } from '../ui/Glyph'
import { Icon } from '../ui/Icon'
import { Kbd } from '../ui/layout'
import { Menu } from '../ui/Menu'
import { Sheet } from '../ui/Sheet'
import { AREAS } from './areas'
import { navigate } from './nav'
import { SHORTCUTS, shortcutsOpen } from './shortcuts'

export const moreOpen = signal(false)

const THEME_OPTIONS: Array<{ value: ThemePref; label: string; icon: string }> = [
  { value: 'carbon', label: 'Carbon', icon: 'moon' },
  { value: 'paper', label: 'Paper', icon: 'sun' },
  { value: 'auto', label: 'Auto', icon: 'sun-moon' },
]

export function ThemeSegmented({ size = 'md' }: { size?: 'sm' | 'md' }) {
  return (
    <Segmented
      label="Theme"
      size={size}
      value={themePref.value}
      onChange={(v) => setTheme(v)}
      options={THEME_OPTIONS.map((o) => ({ value: o.value, label: o.label, icon: o.icon }))}
    />
  )
}

/** Rail button that opens a theme menu. */
export function ThemeButton() {
  const ref = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const cur = THEME_OPTIONS.find((o) => o.value === themePref.value) ?? THEME_OPTIONS[0]
  return (
    <>
      <button
        ref={ref}
        type="button"
        class="rail-btn"
        aria-label={`Theme: ${cur.label}`}
        aria-haspopup="menu"
        aria-expanded={open}
        title={`Theme: ${cur.label}`}
        onClick={() => setOpen((o) => !o)}
      >
        <Icon name={cur.icon} size={18} />
      </button>
      <Menu
        open={open}
        onClose={() => setOpen(false)}
        anchor={ref.current}
        label="Theme"
        placement="right-start"
        items={THEME_OPTIONS.map((o) => ({
          id: o.value,
          label: o.label,
          icon: o.icon,
          hint: o.value === 'auto' ? 'System' : o.value === 'carbon' ? 'Dark' : 'Light',
          checked: themePref.value === o.value,
          onSelect: () => setTheme(o.value),
        }))}
      />
    </>
  )
}

/** Header pill shown only while the live connection is not healthy. */
export function ConnectionPill() {
  const c = connection.value
  const [now, setNow] = useState(Date.now())
  // Give the first connection a moment before showing anything.
  const [grace, setGrace] = useState(true)
  useEffect(() => {
    const t = window.setTimeout(() => setGrace(false), 1500)
    return () => window.clearTimeout(t)
  }, [])
  useEffect(() => {
    if (c.state !== 'reconnecting') return
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [c.state])
  if (c.state === 'online' || (c.state === 'connecting' && grace)) return null
  const secs = c.retryAt ? Math.max(0, Math.ceil((c.retryAt - now) / 1000)) : 0
  const offline = c.state === 'offline'
  return (
    <div class={cx('conn-pill', offline && 'conn-pill--offline')} role="status">
      <Icon name={offline ? 'wifi-off' : 'refresh-cw'} size={14} class={cx(!offline && 'conn-pill__spin')} />
      <span class="conn-pill__long">
        {offline
          ? 'Offline'
          : c.state === 'connecting'
            ? 'Connecting…'
            : secs > 1
              ? `Reconnecting in ${secs}s`
              : 'Reconnecting…'}
      </span>
      {/* Phones: the header is narrow, so only the countdown shows (the role=status text above stays for AT). */}
      <span class="conn-pill__short" aria-hidden="true">
        {offline ? 'Offline' : secs > 1 ? `${secs}s` : '…'}
      </span>
      {!offline && c.state !== 'connecting' && (
        <button type="button" class="conn-pill__retry" onClick={retryConnection}>
          Retry
        </button>
      )}
    </div>
  )
}

/** Small status dot for the rail foot (machine reachability). */
export function DeviceDot() {
  const c = connection.value.state
  const host = info.value?.hostname ?? 'this machine'
  const label =
    c === 'online' ? `Connected to ${host}` : c === 'offline' ? 'Offline' : `Reconnecting to ${host}`
  return (
    <span class={cx('device-dot', `device-dot--${c}`)} title={label} role="img" aria-label={label}>
      <span />
    </span>
  )
}

/** Mobile "More": the areas without a tab, theme, shortcuts, sign out. */
export function MoreSheet() {
  const extra = AREAS.filter((a) => !a.mobileTab)
  const go = (path: string) => {
    moreOpen.value = false
    navigate(path)
  }
  const i = info.value
  return (
    <Sheet open={moreOpen.value} onClose={() => (moreOpen.value = false)} title="More" side="bottom">
      <nav class="more-grid" aria-label="More areas">
        {extra.map((a) => (
          <a
            key={a.id}
            href={a.path}
            class="more-tile"
            style={{ '--hue': a.hue }}
            onClick={(e) => {
              e.preventDefault()
              go(a.path)
            }}
          >
            <Glyph name={a.id} size={26} color="var(--hue)" />
            <span class="more-tile__label">{a.label}</span>
            <span class="more-tile__blurb">{a.blurb}</span>
          </a>
        ))}
      </nav>
      <div class="more-section">
        <span class="more-section__label">Appearance</span>
        <ThemeSegmented />
      </div>
      <div class="more-actions">
        <Button variant="secondary" size="lg" icon="log-out" onClick={() => void signOut()}>
          Sign out
        </Button>
      </div>
      {i && (
        <p class="more-foot">
          {i.hostname} · Relay {i.version}
        </p>
      )}
    </Sheet>
  )
}

/** The `?` sheet. */
export function ShortcutSheet() {
  const groups = ['General', 'Go to', 'Command center'] as const
  return (
    <Dialog
      open={shortcutsOpen.value}
      onClose={() => (shortcutsOpen.value = false)}
      title="Keyboard shortcuts"
      size="md"
    >
      <div class="shortcuts">
        {groups.map((g) => (
          <section key={g} class="shortcuts__group">
            <h3 class="shortcuts__title">{g}</h3>
            <dl>
              {SHORTCUTS.filter((s) => s.group === g).map((s) => (
                <div class="shortcuts__row" key={s.label}>
                  <dt>{s.label}</dt>
                  <dd>
                    {s.keys.map((k, i) => (
                      <span key={k.join('+')} class="shortcuts__seq">
                        {i > 0 && <span class="shortcuts__then">{g === 'Go to' ? 'then' : 'or'}</span>}
                        <Kbd keys={k} />
                      </span>
                    ))}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
      <p class="shortcuts__note">
        {isMac ? '⌘' : 'Ctrl'} shortcuts inside a terminal go to the terminal first. Terminal keys are listed
        in the terminal menu.
      </p>
    </Dialog>
  )
}
