// Form controls: Field (label + hint + error), Input, TextArea, Select,
// Switch, Checkbox, Segmented, Slider. Controls inside a <Field> pick up
// its id and aria wiring automatically.
import { type ComponentChildren, createContext, type JSX, type Ref } from 'preact'
import { forwardRef } from 'preact/compat'
import { useContext, useLayoutEffect, useMemo, useRef } from 'preact/hooks'
import { cx, nextId } from '../lib/util'
import { Icon } from './Icon'
import './controls.css'

interface FieldCtx {
  id: string
  describedBy?: string
  invalid: boolean
}
const FieldContext = createContext<FieldCtx | null>(null)

function useFieldProps(id?: string, invalid?: boolean) {
  const ctx = useContext(FieldContext)
  return {
    id: id ?? ctx?.id,
    'aria-describedby': ctx?.describedBy,
    'aria-invalid': invalid || ctx?.invalid || undefined,
  }
}

export interface FieldProps {
  label: ComponentChildren
  /** Quiet help text under the control. */
  hint?: ComponentChildren
  /** Error text; marks the control invalid (aria-invalid). */
  error?: ComponentChildren
  /** Visually hide the label (still read by screen readers). */
  hideLabel?: boolean
  /** Extra content right-aligned in the label row (e.g. "Forgot?"). */
  aside?: ComponentChildren
  id?: string
  class?: string
  children: ComponentChildren
}

/** Label + control + hint/error, wired for assistive tech. */
export function Field({ label, hint, error, hideLabel, aside, id, class: className, children }: FieldProps) {
  const fid = useMemo(() => id ?? nextId('field'), [id])
  const hintId = hint ? `${fid}-hint` : undefined
  const errId = error ? `${fid}-err` : undefined
  const describedBy = [errId, hintId].filter(Boolean).join(' ') || undefined
  const ctx = useMemo(() => ({ id: fid, describedBy, invalid: !!error }), [fid, describedBy, error])
  return (
    <div class={cx('field', error && 'field--invalid', className)}>
      <div class={cx('field__head', hideLabel && 'sr-only')}>
        <label class="field__label" for={fid}>
          {label}
        </label>
        {aside && <span class="field__aside">{aside}</span>}
      </div>
      <FieldContext.Provider value={ctx}>{children}</FieldContext.Provider>
      {error && (
        <p class="field__error" id={errId} role="alert">
          <Icon name="circle-alert" size={14} />
          {error}
        </p>
      )}
      {hint && !error && (
        <p class="field__hint" id={hintId}>
          {hint}
        </p>
      )}
    </div>
  )
}

export interface InputProps extends Omit<JSX.InputHTMLAttributes<HTMLInputElement>, 'size' | 'icon'> {
  size?: 'sm' | 'md' | 'lg'
  /** Leading icon inside the field. */
  icon?: string
  /** Trailing content inside the field (e.g. a show-password button). */
  trailing?: ComponentChildren
  invalid?: boolean
  mono?: boolean
}

/** Text input. Font size is ≥16 px on touch so iOS never zooms on focus. */
export const Input = forwardRef(function Input(
  { size = 'md', icon, trailing, invalid, mono, class: className, id, ...rest }: InputProps,
  ref: Ref<HTMLInputElement>,
) {
  const fp = useFieldProps(id as string | undefined, invalid)
  return (
    <span
      class={cx(
        'input',
        `input--${size}`,
        mono && 'input--mono',
        !!icon && 'input--icon',
        className as string,
      )}
    >
      {icon && <Icon name={icon} size={16} class="input__icon" />}
      <input ref={ref} class="input__el" {...fp} {...rest} />
      {trailing && <span class="input__trailing">{trailing}</span>}
    </span>
  )
})

export interface TextAreaProps extends JSX.TextareaHTMLAttributes<HTMLTextAreaElement> {
  invalid?: boolean
  mono?: boolean
  /** Grow with content up to this many rows (default 8). */
  maxRows?: number
  autoGrow?: boolean
}

/** Multi-line input, optionally auto-growing. */
export const TextArea = forwardRef(function TextArea(
  { invalid, mono, autoGrow = false, maxRows = 8, class: className, id, onInput, ...rest }: TextAreaProps,
  ref: Ref<HTMLTextAreaElement>,
) {
  const fp = useFieldProps(id as string | undefined, invalid)
  const grow = (el: HTMLTextAreaElement) => {
    if (!autoGrow) return
    el.style.height = 'auto'
    const lh = Number.parseFloat(getComputedStyle(el).lineHeight) || 20
    el.style.height = `${Math.min(el.scrollHeight, lh * maxRows + 16)}px`
  }
  return (
    <textarea
      ref={ref}
      class={cx('textarea', mono && 'textarea--mono', className as string)}
      {...fp}
      onInput={(e) => {
        grow(e.currentTarget)
        onInput?.(e)
      }}
      {...rest}
    />
  )
})

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
}

export interface SelectProps
  extends Omit<JSX.HTMLAttributes<HTMLSelectElement>, 'size' | 'onChange' | 'value'> {
  options: SelectOption[]
  value: string
  onChange: (value: string) => void
  size?: 'sm' | 'md'
  invalid?: boolean
}

/** Native select, styled. Native keeps mobile pickers and accessibility. */
export function Select({
  options,
  value,
  onChange,
  size = 'md',
  invalid,
  class: className,
  id,
  ...rest
}: SelectProps) {
  const fp = useFieldProps(id as string | undefined, invalid)
  return (
    <span class={cx('select', `select--${size}`, className as string)}>
      <select
        class="select__el"
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
        {...fp}
        {...rest}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} disabled={o.disabled}>
            {o.label}
          </option>
        ))}
      </select>
      <svg class="select__chev" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
        <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" />
      </svg>
    </span>
  )
}

export interface SwitchProps {
  checked: boolean
  onChange: (checked: boolean) => void
  /** Visible label (omit inside a Field, which labels it). */
  label?: ComponentChildren
  disabled?: boolean
  id?: string
  class?: string
}

/** On/off toggle (role=switch). */
export function Switch({ checked, onChange, label, disabled, id, class: className }: SwitchProps) {
  const fp = useFieldProps(id)
  const control = (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      class={cx('switch', checked && 'switch--on', !label && className)}
      onClick={() => onChange(!checked)}
      id={fp.id}
      aria-describedby={fp['aria-describedby']}
    >
      <span class="switch__thumb" />
    </button>
  )
  if (!label) return control
  return (
    <label class={cx('check-row', className)}>
      {control}
      <span class="check-row__label">{label}</span>
    </label>
  )
}

export interface CheckboxProps {
  checked: boolean
  onChange: (checked: boolean) => void
  label?: ComponentChildren
  indeterminate?: boolean
  disabled?: boolean
  id?: string
  class?: string
}

/** Checkbox with a proper label hit area. */
export function Checkbox({
  checked,
  onChange,
  label,
  indeterminate,
  disabled,
  id,
  class: className,
}: CheckboxProps) {
  const fp = useFieldProps(id)
  const ref = useRef<HTMLInputElement>(null)
  useLayoutEffect(() => {
    if (ref.current) ref.current.indeterminate = !!indeterminate
  }, [indeterminate])
  return (
    <label class={cx('check-row', disabled && 'check-row--disabled', className)}>
      <span class="checkbox">
        <input
          ref={ref}
          type="checkbox"
          class="checkbox__el"
          checked={checked}
          disabled={disabled}
          onChange={(e) => onChange(e.currentTarget.checked)}
          {...fp}
        />
        <svg class="checkbox__mark" viewBox="0 0 16 16" aria-hidden="true">
          {indeterminate ? <path d="M4 8h8" /> : <path d="M3.5 8.5l3 3 6-7" />}
        </svg>
      </span>
      {label && <span class="check-row__label">{label}</span>}
    </label>
  )
}

export interface SegmentedOption<T extends string> {
  value: T
  label: ComponentChildren
  icon?: string
  /** Accessible label when `label` is not text. */
  aria?: string
}

export interface SegmentedProps<T extends string> {
  options: SegmentedOption<T>[]
  value: T
  onChange: (value: T) => void
  /** Accessible group name. */
  label: string
  size?: 'sm' | 'md'
  class?: string
}

/** A small exclusive choice (radio group) — e.g. Carbon / Paper / Auto. */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  label,
  size = 'md',
  class: className,
}: SegmentedProps<T>) {
  const idx = Math.max(
    0,
    options.findIndex((o) => o.value === value),
  )
  const onKey = (e: KeyboardEvent) => {
    const d =
      e.key === 'ArrowRight' || e.key === 'ArrowDown'
        ? 1
        : e.key === 'ArrowLeft' || e.key === 'ArrowUp'
          ? -1
          : 0
    if (!d) return
    e.preventDefault()
    const next = options[(idx + d + options.length) % options.length]
    onChange(next.value)
    const el = (e.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role=radio]')[
      (idx + d + options.length) % options.length
    ]
    el?.focus()
  }
  return (
    <div
      class={cx('segmented', `segmented--${size}`, className)}
      role="radiogroup"
      aria-label={label}
      onKeyDown={onKey}
      style={{ '--n': options.length, '--i': idx }}
    >
      <span class="segmented__thumb" aria-hidden="true" />
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          aria-label={o.aria}
          tabIndex={o.value === value ? 0 : -1}
          class={cx('segmented__opt', o.value === value && 'is-on')}
          onClick={() => onChange(o.value)}
        >
          {o.icon && <Icon name={o.icon} size={14} />}
          {o.label}
        </button>
      ))}
    </div>
  )
}

export interface SliderProps {
  value: number
  onChange: (value: number) => void
  min?: number
  max?: number
  step?: number
  /** Accessible name (omit inside a Field). */
  label?: string
  /** Formats the value readout; omit to hide it. */
  format?: (v: number) => string
  disabled?: boolean
  id?: string
  class?: string
}

/** Range slider with an optional mono readout. */
export function Slider({
  value,
  onChange,
  min = 0,
  max = 100,
  step = 1,
  label,
  format,
  disabled,
  id,
  class: className,
}: SliderProps) {
  const fp = useFieldProps(id)
  const pct = ((value - min) / (max - min || 1)) * 100
  return (
    <span class={cx('slider', className)} style={{ '--pct': `${pct}%` }}>
      <input
        type="range"
        class="slider__el"
        min={min}
        max={max}
        step={step}
        value={value}
        disabled={disabled}
        aria-label={label}
        aria-valuetext={format ? format(value) : undefined}
        onInput={(e) => onChange(Number(e.currentTarget.value))}
        {...fp}
      />
      {format && <output class="slider__value">{format(value)}</output>}
    </span>
  )
}
