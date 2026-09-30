import type { ComponentChildren, JSX, Ref } from 'preact'
import { forwardRef } from 'preact/compat'
import { cx } from '../lib/util'
import { Icon } from './Icon'
import { Spinner } from './Spinner'
import './controls.css'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'icon'
export type ButtonSize = 'sm' | 'md' | 'lg'

export interface ButtonProps
  extends Omit<JSX.HTMLAttributes<HTMLButtonElement>, 'size' | 'icon' | 'loading'> {
  variant?: ButtonVariant
  /** sm 28 / md 34 (40 on touch) / lg 44 px. */
  size?: ButtonSize
  /** Leading icon name (see Icon). */
  icon?: string
  /** Trailing icon name. */
  iconEnd?: string
  /** Shows a spinner and disables the button; keeps its width. */
  loading?: boolean
  /** Required for variant="icon" (becomes aria-label + tooltip). */
  label?: string
  /** Render as a link (<a>) instead of a button. */
  href?: string
  type?: 'button' | 'submit' | 'reset'
  disabled?: boolean
  children?: ComponentChildren
}

/**
 * The one button. `primary` is the signal-orange "act now" action — use
 * at most one per screen. `icon` renders a square icon-only button and
 * requires `label`.
 */
export const Button = forwardRef(function Button(
  {
    variant = 'secondary',
    size = 'md',
    icon,
    iconEnd,
    loading = false,
    label,
    href,
    type = 'button',
    disabled,
    class: className,
    children,
    ...rest
  }: ButtonProps,
  ref: Ref<HTMLButtonElement>,
) {
  const cls = cx('btn', `btn--${variant}`, `btn--${size}`, loading && 'btn--loading', className as string)
  const iconSize = size === 'sm' ? 14 : size === 'lg' ? 18 : 16
  const body = (
    <>
      {icon && <Icon name={icon} size={iconSize} class="btn__icon" />}
      {children !== undefined && variant !== 'icon' && <span class="btn__label">{children}</span>}
      {iconEnd && <Icon name={iconEnd} size={iconSize} class="btn__icon" />}
      {loading && (
        <span class="btn__spinner">
          <Spinner size="sm" label="Working" />
        </span>
      )}
    </>
  )
  if (href && !disabled) {
    return (
      <a
        class={cls}
        href={href}
        aria-label={variant === 'icon' ? label : undefined}
        title={variant === 'icon' ? label : undefined}
        {...(rest as JSX.HTMLAttributes<HTMLAnchorElement>)}
      >
        {body}
      </a>
    )
  }
  return (
    <button
      ref={ref}
      type={type}
      class={cls}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      aria-label={variant === 'icon' ? label : rest['aria-label']}
      title={variant === 'icon' ? label : (rest.title as string | undefined)}
      {...rest}
    >
      {body}
    </button>
  )
})
