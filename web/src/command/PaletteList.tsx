// Keyboard-navigable result list used by the palette root, list views,
// custom views and the action panel. Rendering + selection live here; the
// palette's search field forwards ↑ ↓ Enter ⌘Enter → to the active list
// through PaletteContext, so focus never leaves the input.
import { type ComponentChildren, createContext } from 'preact'
import { useContext, useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks'
import { cx } from '../lib/util'
import { Icon } from '../ui/Icon'
import { Kbd } from '../ui/layout'
import { StatusDot } from '../ui/StatusDot'
import { highlightRuns, type Ranked, type Section } from './rank'
import type { CommandContext, PaletteItem } from './registry'

export interface ListController {
  move: (delta: number) => void
  edge: (end: 'first' | 'last') => void
  /** Run the selected item (secondary = its first extra action). */
  run: (secondary: boolean) => boolean
  selected: () => PaletteItem | null
}

export interface PaletteApi {
  ctx: CommandContext
  /** Run an item's primary action, or action at `actionIndex`. */
  runItem: (item: PaletteItem, actionIndex?: number) => void
  /** Open the action panel for an item. */
  openActions: (item: PaletteItem) => void
  setController: (slot: 'list' | 'panel', c: ListController | null) => void
  onSelect: (item: PaletteItem | null, optionId?: string) => void
  /** Touch layout: show the ⋯ affordance on rows. */
  touch: boolean
  listId: string
}

export const PaletteContext = createContext<PaletteApi | null>(null)

export interface PaletteListProps {
  sections: Section[]
  /** Show shimmer rows under this label (e.g. "Searching this machine…"). */
  loading?: string | false
  /** Rendered when there are no sections and not loading. */
  empty?: ComponentChildren
  /** 'panel' for the action panel (runs via onRun). */
  slot?: 'list' | 'panel'
  onRun?: (item: PaletteItem, secondary: boolean) => void
  /** Accessible name. */
  label?: string
  /** Reset the selection to the top when this changes (e.g. the query). */
  resetKey?: unknown
}

const optionId = (listId: string, i: number) => `${listId}-o${i}`

/** Sectioned, keyboard-driven list of palette items. */
export function PaletteList({
  sections,
  loading,
  empty,
  slot = 'list',
  onRun,
  label = 'Results',
  resetKey,
}: PaletteListProps) {
  const api = useContext(PaletteContext)
  const flat: Ranked[] = []
  for (const s of sections) for (const r of s.items) flat.push(r)
  const [sel, setSel] = useState(0)
  const selRef = useRef(0)
  const flatRef = useRef(flat)
  flatRef.current = flat
  const root = useRef<HTMLDivElement>(null)
  const pointerMoved = useRef(false)
  const listId = slot === 'panel' ? `${api?.listId}-panel` : (api?.listId ?? 'palette-list')

  const clamp = (i: number) =>
    flatRef.current.length ? Math.max(0, Math.min(flatRef.current.length - 1, i)) : 0
  const select = (i: number) => {
    selRef.current = clamp(i)
    setSel(selRef.current)
  }

  useLayoutEffect(() => select(0), [resetKey])
  // Keep the selection valid when the list shrinks.
  if (sel > 0 && sel >= flat.length) {
    selRef.current = clamp(sel)
  }
  const current = flat[clamp(selRef.current)]?.item ?? null

  const run = (item: PaletteItem, secondary: boolean) => {
    if (onRun) onRun(item, secondary)
    else if (api) api.runItem(item, secondary ? 0 : undefined)
  }

  useEffect(() => {
    if (!api) return
    const c: ListController = {
      move: (d) => {
        const n = flatRef.current.length
        if (!n) return
        select((selRef.current + d + n) % n)
      },
      edge: (end) => select(end === 'first' ? 0 : flatRef.current.length - 1),
      run: (secondary) => {
        const it = flatRef.current[clamp(selRef.current)]?.item
        if (!it) return false
        if (secondary && !it.actions?.length) return false
        run(it, secondary)
        return true
      },
      selected: () => flatRef.current[clamp(selRef.current)]?.item ?? null,
    }
    api.setController(slot, c)
    return () => api.setController(slot, null)
  })

  useEffect(() => {
    if (slot === 'list') api?.onSelect(current, current ? optionId(listId, clamp(selRef.current)) : undefined)
  }, [current?.id, sel, slot])

  useLayoutEffect(() => {
    root.current
      ?.querySelector(`#${CSS.escape(optionId(listId, clamp(selRef.current)))}`)
      ?.scrollIntoView({ block: 'nearest' })
  }, [sel, flat.length])

  if (!flat.length && !loading) return <div class="pal-empty-wrap">{empty}</div>

  let index = 0
  return (
    <div
      ref={root}
      class={cx('pal-list', slot === 'panel' && 'pal-list--panel')}
      id={listId}
      role="listbox"
      aria-label={label}
      onPointerMove={() => {
        pointerMoved.current = true
      }}
    >
      {sections.map((s) => (
        // biome-ignore lint/a11y/useSemanticElements: listbox option groups must be role=group
        <div class="pal-section" role="group" aria-label={s.title} key={s.title}>
          {slot === 'list' && (
            <div class="pal-section__title" aria-hidden="true">
              {s.title}
            </div>
          )}
          {s.items.map((r) => {
            const i = index++
            return (
              <PaletteRow
                key={r.item.id}
                id={optionId(listId, i)}
                ranked={r}
                active={i === clamp(selRef.current)}
                touch={!!api?.touch && slot === 'list'}
                onHover={() => {
                  if (pointerMoved.current) select(i)
                }}
                onRun={() => {
                  select(i)
                  run(r.item, false)
                }}
                onActions={api && slot === 'list' ? () => api.openActions(r.item) : undefined}
              />
            )
          })}
        </div>
      ))}
      {loading && (
        <div class="pal-section" aria-busy="true">
          <div class="pal-section__title">{loading}</div>
          {[0, 1, 2].map((k) => (
            <div class="pal-row pal-row--shimmer" key={k} aria-hidden="true">
              <span class="pal-row__icon" />
              <span class="pal-row__main">
                <span class="pal-shimmer" style={{ width: `${46 - k * 9}%` }} />
              </span>
            </div>
          ))}
          <span class="sr-only" role="status">
            {loading}
          </span>
        </div>
      )}
    </div>
  )
}

interface RowProps {
  id: string
  ranked: Ranked
  active: boolean
  touch: boolean
  onHover: () => void
  onRun: () => void
  onActions?: () => void
}

function PaletteRow({ id, ranked, active, touch, onHover, onRun, onActions }: RowProps) {
  const { item, indexes } = ranked
  const hasActions = !!item.actions?.length
  return (
    <div
      id={id}
      role="option"
      aria-selected={active}
      tabIndex={-1}
      class={cx(
        'pal-row',
        active && 'is-active',
        item.actions?.some((a) => a.danger) && 'pal-row--has-danger',
      )}
      onPointerEnter={onHover}
      onClick={onRun}
      onKeyDown={(e) => {
        if (e.key === 'Enter') onRun()
      }}
    >
      <span class="pal-row__icon">
        {item.icon ? (
          <Icon name={item.icon} size={item.icon.startsWith('glyph:') ? 16 : 16} />
        ) : (
          <Icon name="command" />
        )}
        {item.status && <StatusDot status={item.status} size="sm" class="pal-row__status" />}
      </span>
      <span class="pal-row__main">
        <span class="pal-row__title">
          {highlightRuns(item.title, indexes).map((run, k) =>
            run.hit ? <mark key={k}>{run.text}</mark> : run.text,
          )}
        </span>
        {item.subtitle && <span class="pal-row__sub">{item.subtitle}</span>}
      </span>
      <span class="pal-row__end">
        {item.accessory && <span class="pal-row__acc">{item.accessory}</span>}
        {item.shortcut && !touch && <Kbd keys={item.shortcut} class="pal-row__kbd" />}
        {item.view && <Icon name="chevron-right" class="pal-row__chev" />}
        {touch && hasActions && onActions && (
          <button
            type="button"
            class="pal-row__more"
            aria-label={`Actions for ${item.title}`}
            onClick={(e) => {
              e.stopPropagation()
              onActions()
            }}
          >
            <Icon name="ellipsis" size={18} />
          </button>
        )}
      </span>
    </div>
  )
}
