// Surfaces and small display pieces: Card, Panel, List, ListRow, Table,
// Badge, Tag, Kbd, EmptyState, Skeleton, Progress, ProgressRing, Tabs,
// PathBar.
import type { ComponentChildren, JSX } from 'preact'
import { useRef } from 'preact/hooks'
import { keyLabels } from '../lib/keys'
import { clamp, cx } from '../lib/util'
import { Glyph } from './Glyph'
import type { GlyphName } from './glyphs'
import { Icon } from './Icon'
import './surfaces.css'

// ------------------------------------------------------------ Card / Panel

export interface CardProps extends JSX.HTMLAttributes<HTMLDivElement> {
  /** Makes the whole card a link (keyboard + pointer) — use with care. */
  href?: string
  /** Padding: none / sm 12 / md 16 (default) / lg 24. */
  pad?: 'none' | 'sm' | 'md' | 'lg'
  /** Emphasise with the signal border (use for "needs you"). */
  tone?: 'default' | 'signal' | 'danger' | 'sunken'
  children?: ComponentChildren
}

/** A rounded surface-1 container with a hairline. */
export function Card({ href, pad = 'md', tone = 'default', class: className, children, ...rest }: CardProps) {
  const cls = cx(
    'card',
    `card--pad-${pad}`,
    tone !== 'default' && `card--${tone}`,
    href && 'card--link',
    className as string,
  )
  if (href)
    return (
      <a href={href} class={cls} {...(rest as JSX.HTMLAttributes<HTMLAnchorElement>)}>
        {children}
      </a>
    )
  return (
    <div class={cls} {...rest}>
      {children}
    </div>
  )
}

export interface PanelProps {
  /** Section heading (rendered as h2 by default). */
  title?: ComponentChildren
  /** Quiet mono text right of the title (counts, timestamps). */
  meta?: ComponentChildren
  /** Actions at the right of the header. */
  actions?: ComponentChildren
  /** Heading level for document outline. */
  level?: 2 | 3
  class?: string
  /** Remove the inner padding (for lists and tables that touch the edges). */
  flush?: boolean
  children?: ComponentChildren
}

/** A titled section surface: header row + body. */
export function Panel({ title, meta, actions, level = 2, class: className, flush, children }: PanelProps) {
  const H = level === 2 ? 'h2' : 'h3'
  return (
    <section class={cx('panel', flush && 'panel--flush', className)}>
      {(title || actions) && (
        <header class="panel__head">
          {title && <H class="panel__title">{title}</H>}
          {meta && <span class="panel__meta t-mono">{meta}</span>}
          {actions && <div class="panel__actions">{actions}</div>}
        </header>
      )}
      <div class="panel__body">{children}</div>
    </section>
  )
}

// ------------------------------------------------------------ List

export interface ListProps {
  /** Accessible name for the list. */
  label?: string
  /** Inset dividers (default) or none. */
  dividers?: boolean
  class?: string
  children?: ComponentChildren
}

/** Vertical list of ListRows. */
export function List({ label, dividers = true, class: className, children }: ListProps) {
  return (
    <ul class={cx('list', dividers && 'list--dividers', className)} aria-label={label}>
      {children}
    </ul>
  )
}

export interface ListRowProps {
  /** Leading visual: Icon name string or any node (AgentMark, StatusDot…). */
  leading?: ComponentChildren | string
  title: ComponentChildren
  /** Second line (plain words). */
  subtitle?: ComponentChildren
  /** Instrument-layer meta (mono), shown right-aligned before trailing. */
  meta?: ComponentChildren
  trailing?: ComponentChildren
  /** Navigate on activate. */
  href?: string
  onClick?: (e: MouseEvent) => void
  /** Context menu / long-press handler (see useLongPress / Menu). */
  onContextMenu?: (e: MouseEvent) => void
  selected?: boolean
  disabled?: boolean
  class?: string
}

/** A 44 px+ row: leading, title/subtitle, meta, trailing. */
export function ListRow({
  leading,
  title,
  subtitle,
  meta,
  trailing,
  href,
  onClick,
  onContextMenu,
  selected,
  disabled,
  class: className,
}: ListRowProps) {
  const inner = (
    <>
      {leading !== undefined && (
        <span class="row__leading">
          {typeof leading === 'string' ? <Icon name={leading} size={18} /> : leading}
        </span>
      )}
      <span class="row__main">
        <span class="row__title">{title}</span>
        {subtitle && <span class="row__subtitle">{subtitle}</span>}
      </span>
      {meta && <span class="row__meta t-mono">{meta}</span>}
      {trailing && <span class="row__trailing">{trailing}</span>}
    </>
  )
  const cls = cx(
    'row-item',
    (href || onClick) && 'row-item--interactive',
    selected && 'is-selected',
    className,
  )
  return (
    <li class="list__item" onContextMenu={onContextMenu}>
      {href ? (
        <a
          class={cls}
          href={href}
          aria-current={selected ? 'true' : undefined}
          aria-disabled={disabled || undefined}
        >
          {inner}
        </a>
      ) : onClick ? (
        <button
          type="button"
          class={cls}
          onClick={onClick}
          disabled={disabled}
          aria-pressed={selected || undefined}
        >
          {inner}
        </button>
      ) : (
        <div class={cls}>{inner}</div>
      )}
    </li>
  )
}

// ------------------------------------------------------------ Table

export interface Column<T> {
  key: string
  header: ComponentChildren
  render: (row: T) => ComponentChildren
  /** Right-align + tabular nums. */
  numeric?: boolean
  /** CSS width, e.g. "120px" or "30%". */
  width?: string
  /** Hide below this breakpoint ('sm' 480 / 'md' 768). */
  hideBelow?: 'sm' | 'md'
}

export interface TableProps<T> {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  onRowClick?: (row: T) => void
  /** Accessible caption (visually hidden). */
  caption: string
  empty?: ComponentChildren
  class?: string
}

/** Dense data table (processes, services). */
export function Table<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  caption,
  empty,
  class: className,
}: TableProps<T>) {
  return (
    <div class={cx('table-wrap', className)}>
      <table class="table">
        <caption class="sr-only">{caption}</caption>
        <thead>
          <tr>
            {columns.map((c) => (
              <th
                key={c.key}
                scope="col"
                class={cx(c.numeric && 'num', c.hideBelow && `hide-${c.hideBelow}`)}
                style={c.width ? { width: c.width } : undefined}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && empty ? (
            <tr>
              <td colSpan={columns.length} class="table__empty">
                {empty}
              </td>
            </tr>
          ) : (
            rows.map((r) => (
              <tr
                key={rowKey(r)}
                class={onRowClick ? 'is-clickable' : undefined}
                onClick={onRowClick ? () => onRowClick(r) : undefined}
                tabIndex={onRowClick ? 0 : undefined}
                onKeyDown={
                  onRowClick
                    ? (e) => {
                        if (e.key === 'Enter') onRowClick(r)
                      }
                    : undefined
                }
              >
                {columns.map((c) => (
                  <td key={c.key} class={cx(c.numeric && 'num', c.hideBelow && `hide-${c.hideBelow}`)}>
                    {c.render(r)}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  )
}

// ------------------------------------------------------------ Badge / Tag / Kbd

export interface BadgeProps {
  tone?: 'neutral' | 'signal' | 'ok' | 'warn' | 'danger' | 'info'
  /** Solid fill (counts on the bell) vs soft tint (default). */
  solid?: boolean
  class?: string
  children: ComponentChildren
}

/** Small status pill or count. */
export function Badge({ tone = 'neutral', solid = false, class: className, children }: BadgeProps) {
  return <span class={cx('badge', `badge--${tone}`, solid && 'badge--solid', className)}>{children}</span>
}

export interface TagProps {
  children: ComponentChildren
  /** Optional dot colour (area hue or agent colour). */
  color?: string
  /** Shows a remove button with this accessible label. */
  onRemove?: () => void
  removeLabel?: string
  class?: string
}

/** A quiet mono label (branch, language, model). */
export function Tag({ children, color, onRemove, removeLabel = 'Remove', class: className }: TagProps) {
  return (
    <span class={cx('tag', className)}>
      {color && <i class="tag__dot" style={{ background: color }} aria-hidden="true" />}
      {children}
      {onRemove && (
        <button type="button" class="tag__remove" onClick={onRemove} aria-label={removeLabel}>
          <Icon name="x" size={12} />
        </button>
      )}
    </span>
  )
}

export interface KbdProps {
  /** Keys, e.g. ['mod','K'] → ⌘ K on Mac, Ctrl K elsewhere. */
  keys: string[]
  class?: string
}

/** Keyboard shortcut hint. */
export function Kbd({ keys, class: className }: KbdProps) {
  const labels = keyLabels(keys)
  return (
    <span class={cx('kbd', className)} role="img" aria-label={labels.join(' ')}>
      {labels.map((k, i) => (
        <kbd key={i}>{k}</kbd>
      ))}
    </span>
  )
}

// ------------------------------------------------------------ EmptyState / Skeleton

export interface EmptyStateProps {
  /** Area glyph shown large in dot-matrix. */
  glyph?: GlyphName
  /** Glyph colour (use the area hue). */
  hue?: string
  title: ComponentChildren
  /** One sentence. */
  body?: ComponentChildren
  /** One primary action (a Button). */
  action?: ComponentChildren
  size?: 'sm' | 'md'
  class?: string
}

/** Empty states are invitations: one sentence + one primary action. */
export function EmptyState({
  glyph = 'empty',
  hue,
  title,
  body,
  action,
  size = 'md',
  class: className,
}: EmptyStateProps) {
  return (
    <div class={cx('empty', `empty--${size}`, className)}>
      <Glyph name={glyph} size={size === 'sm' ? 32 : 56} color={hue} state="active" class="empty__glyph" />
      <p class="empty__title">{title}</p>
      {body && <p class="empty__body">{body}</p>}
      {action && <div class="empty__action">{action}</div>}
    </div>
  )
}

export interface SkeletonProps {
  /** CSS width (default 100%). */
  width?: string | number
  /** CSS height (default 14 px, a text line). */
  height?: string | number
  /** Number of stacked lines (last one shorter). */
  lines?: number
  radius?: string
  class?: string
}

/** Scan-line shimmer placeholder. Shown within 100 ms of a slow load. */
export function Skeleton({ width, height = 14, lines = 1, radius, class: className }: SkeletonProps) {
  if (lines > 1)
    return (
      <span class={cx('skeleton-stack', className)} aria-hidden="true">
        {Array.from({ length: lines }, (_, i) => (
          <span
            key={i}
            class="skeleton"
            style={{ width: i === lines - 1 ? '62%' : (width ?? '100%'), height, borderRadius: radius }}
          />
        ))}
      </span>
    )
  return (
    <span
      class={cx('skeleton', className)}
      aria-hidden="true"
      style={{ width: width ?? '100%', height, borderRadius: radius }}
    />
  )
}

// ------------------------------------------------------------ Progress

export interface ProgressProps {
  /** Fraction 0–1; omit for indeterminate. */
  value?: number
  label: string
  tone?: 'default' | 'signal' | 'ok' | 'warn' | 'danger'
  class?: string
}

/** Thin progress bar (uploads, installs). */
export function Progress({ value, label, tone = 'default', class: className }: ProgressProps) {
  const v = value === undefined ? undefined : clamp(value, 0, 1)
  return (
    <span
      class={cx('progress', `progress--${tone}`, v === undefined && 'progress--indeterminate', className)}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={v === undefined ? undefined : Math.round(v * 100)}
    >
      <span class="progress__bar" style={v === undefined ? undefined : { transform: `scaleX(${v})` }} />
    </span>
  )
}

export interface ProgressRingProps {
  value: number
  size?: number
  stroke?: number
  label: string
  tone?: ProgressProps['tone']
  /** Text in the middle (e.g. "42%"). */
  children?: ComponentChildren
  class?: string
}

/** Circular progress (quotas, disk). */
export function ProgressRing({
  value,
  size = 44,
  stroke = 4,
  label,
  tone = 'default',
  children,
  class: className,
}: ProgressRingProps) {
  const v = clamp(value, 0, 1)
  const r = (size - stroke) / 2
  const c = 2 * Math.PI * r
  return (
    <span
      class={cx('ring', `progress--${tone}`, className)}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(v * 100)}
      style={{ width: size, height: size }}
    >
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true">
        <circle class="ring__track" cx={size / 2} cy={size / 2} r={r} stroke-width={stroke} />
        <circle
          class="ring__bar"
          cx={size / 2}
          cy={size / 2}
          r={r}
          stroke-width={stroke}
          stroke-dasharray={c}
          stroke-dashoffset={c * (1 - v)}
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
        />
      </svg>
      {children && <span class="ring__label">{children}</span>}
    </span>
  )
}

// ------------------------------------------------------------ Tabs

export interface TabItem<T extends string> {
  id: T
  label: ComponentChildren
  /** Count badge after the label. */
  count?: number
  /** Link instead of state (keeps tabs in the URL). */
  href?: string
}

export interface TabsProps<T extends string> {
  items: TabItem<T>[]
  value: T
  onChange?: (id: T) => void
  label: string
  class?: string
}

/**
 * Underline tabs. With `href` items they render as links (URL-driven);
 * otherwise as an ARIA tablist with arrow-key navigation.
 */
export function Tabs<T extends string>({ items, value, onChange, label, class: className }: TabsProps<T>) {
  const ref = useRef<HTMLDivElement>(null)
  const links = items.every((i) => i.href)
  const onKey = (e: KeyboardEvent) => {
    if (links || !onChange) return
    const idx = items.findIndex((i) => i.id === value)
    const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0
    if (!d) return
    e.preventDefault()
    const next = (idx + d + items.length) % items.length
    onChange(items[next].id)
    ref.current?.querySelectorAll<HTMLElement>('[role=tab]')[next]?.focus()
  }
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: arrow-key roving for the tablist
    // biome-ignore lint/a11y/useAriaPropsSupportedByRole: aria-label names the tablist or the link group
    <div
      ref={ref}
      class={cx('tabs', className)}
      role={links ? undefined : 'tablist'}
      aria-label={label}
      onKeyDown={onKey}
    >
      {items.map((it) => {
        const on = it.id === value
        const body = (
          <>
            {it.label}
            {it.count !== undefined && <span class="tabs__count">{it.count}</span>}
          </>
        )
        return it.href ? (
          <a
            key={it.id}
            href={it.href}
            class={cx('tabs__tab', on && 'is-on')}
            aria-current={on ? 'page' : undefined}
          >
            {body}
          </a>
        ) : (
          <button
            key={it.id}
            type="button"
            role="tab"
            aria-selected={on}
            tabIndex={on ? 0 : -1}
            class={cx('tabs__tab', on && 'is-on')}
            onClick={() => onChange?.(it.id)}
          >
            {body}
          </button>
        )
      })}
    </div>
  )
}

// ------------------------------------------------------------ PathBar

export interface PathBarProps {
  /** Absolute path, e.g. "/home/me/code/relay". (Named `value`: `path` is reserved by the router.) */
  value: string
  /** Home directory; shown as "~". */
  home?: string
  /** Build the link for a prefix path. */
  hrefFor: (path: string) => string
  class?: string
}

/** Split a path into breadcrumb segments (exported for tests). */
export function pathSegments(p: string, home?: string): Array<{ label: string; path: string }> {
  const clean = p.replace(/\/+$/, '') || '/'
  const segs: Array<{ label: string; path: string }> = []
  let rest = clean
  let base = ''
  if (home && (clean === home || clean.startsWith(`${home}/`))) {
    segs.push({ label: '~', path: home })
    base = home
    rest = clean.slice(home.length)
  } else {
    segs.push({ label: '/', path: '/' })
  }
  for (const part of rest.split('/').filter(Boolean)) {
    base = `${base}/${part}`
    segs.push({ label: part, path: base })
  }
  return segs
}

/** Breadcrumb for files; long paths collapse the middle into "…". */
export function PathBar({ value, home, hrefFor, class: className }: PathBarProps) {
  const segs = pathSegments(value, home)
  const shown = segs.length > 5 ? [segs[0], { label: '…', path: '' }, ...segs.slice(-3)] : segs
  return (
    <nav class={cx('pathbar', className)} aria-label="Path">
      {/* biome-ignore lint/a11y/noRedundantRoles: restores list semantics Safari drops with list-style: none */}
      <ol role="list">
        {shown.map((s, i) => {
          const last = i === shown.length - 1
          return (
            <li key={s.path || `gap${i}`}>
              {s.path === '' ? (
                <span
                  class="pathbar__gap"
                  title={segs
                    .slice(1, -3)
                    .map((x) => x.label)
                    .join('/')}
                >
                  …
                </span>
              ) : last ? (
                <span class="pathbar__seg is-current" aria-current="page">
                  {s.label}
                </span>
              ) : (
                <a class="pathbar__seg" href={hrefFor(s.path)}>
                  {s.label}
                </a>
              )}
              {!last && (
                <span class="pathbar__sep" aria-hidden="true">
                  /
                </span>
              )}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
