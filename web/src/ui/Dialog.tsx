import type { ComponentChildren } from 'preact'
import { useRef } from 'preact/hooks'
import { cx, nextId } from '../lib/util'
import { Button } from './Button'
import { Portal, useDismiss, useFocusTrap, useModal } from './overlay'
import './overlay.css'

export interface DialogProps {
  open: boolean
  onClose: () => void
  title: ComponentChildren
  /** One or two sentences under the title. */
  description?: ComponentChildren
  /** Buttons, right-aligned (primary last). */
  footer?: ComponentChildren
  /** 'alertdialog' for confirmations of destructive actions. */
  role?: 'dialog' | 'alertdialog'
  size?: 'sm' | 'md' | 'lg'
  /** Closing by clicking the scrim (default true; false for forms). */
  dismissable?: boolean
  class?: string
  children?: ComponentChildren
}

/** Centered modal dialog. Esc closes; focus is trapped and restored. */
export function Dialog({
  open,
  onClose,
  title,
  description,
  footer,
  role = 'dialog',
  size = 'md',
  dismissable = true,
  class: className,
  children,
}: DialogProps) {
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useRef(nextId('dlg')).current
  useModal(open)
  useFocusTrap(ref, open)
  useDismiss(ref, open, onClose, { outside: false })
  if (!open) return null
  return (
    <Portal>
      <div class="dialog-layer">
        <div class="scrim scrim--in" onClick={dismissable ? onClose : undefined} aria-hidden="true" />
        <div
          ref={ref}
          class={cx('dialog', `dialog--${size}`, className)}
          role={role}
          aria-modal="true"
          aria-labelledby={titleId}
          data-modal
          tabIndex={-1}
        >
          <header class="dialog__head">
            <h2 class="dialog__title" id={titleId}>
              {title}
            </h2>
            <Button variant="icon" icon="x" label="Close" onClick={onClose} class="dialog__close" />
          </header>
          {description && <p class="dialog__desc">{description}</p>}
          {children && <div class="dialog__body">{children}</div>}
          {footer && <footer class="dialog__foot">{footer}</footer>}
        </div>
      </div>
    </Portal>
  )
}
