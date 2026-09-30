import type { ComponentChildren } from 'preact'
import { cx } from '../lib/util'
import { Button } from './Button'
import './controls.css'

export interface CompactBarProps {
  /** Where "back" goes (default: history back, falling back to "/"). */
  back?: string
  title: ComponentChildren
  /** Quiet line under/after the title (cwd, agent). */
  subtitle?: ComponentChildren
  /** Status node (usually a StatusDot). */
  status?: ComponentChildren
  /** Right-side actions (usually a MenuButton). */
  actions?: ComponentChildren
  /** Make the title a button (e.g. opens the session switcher). */
  onTitleClick?: () => void
  class?: string
}

/**
 * The compact top bar immersive screens (terminal, code, desktop) render
 * on phones, where the app header and tab bar are hidden:
 * ‹ back · title · status · menu. Safe-area aware.
 */
export function CompactBar({
  back,
  title,
  subtitle,
  status,
  actions,
  onTitleClick,
  class: className,
}: CompactBarProps) {
  const goBack = () => {
    if (back) return
    if (history.length > 1) history.back()
    else location.assign('/')
  }
  const titleBody = (
    <>
      <span class="compact-bar__title">{title}</span>
      {subtitle && <span class="compact-bar__sub">{subtitle}</span>}
    </>
  )
  return (
    <header class={cx('compact-bar', className)}>
      <Button
        variant="icon"
        icon="chevron-left"
        label="Back"
        href={back}
        onClick={back ? undefined : goBack}
      />
      {onTitleClick ? (
        <button type="button" class="compact-bar__main" onClick={onTitleClick}>
          {titleBody}
        </button>
      ) : (
        <div class="compact-bar__main">{titleBody}</div>
      )}
      {status}
      {actions}
    </header>
  )
}
