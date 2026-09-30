// The command center (⌘K). Lazy-loaded by the shell on first use.
//
// One search field drives a stack of views: the root (commands + recents +
// provider results) and pushed sub-views (lists or custom components).
// Keys: ↑↓ move · Enter run · ⌘Enter secondary · ⌘K / → action panel ·
// Tab into inline arguments · Esc closes the panel / pops / clears /
// closes · ⌫ on an empty field pops. Phones get a full-screen sheet with
// large rows and a visible Cancel button.
import { useComputed } from '@preact/signals'
import type { ComponentType } from 'preact'
import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { navigate } from '../app/nav'
import { isMac } from '../lib/util'
import { Icon } from '../ui/Icon'
import { Kbd } from '../ui/layout'
import { Portal, useFocusTrap, useMedia, useModal } from '../ui/overlay'
import { toast } from '../ui/Toast'
import { composeRoot, noResultItems, type ProviderResult } from './compose'
import { type ListController, PaletteContext, PaletteList } from './PaletteList'
import { groupRanked, rankItems, type Section } from './rank'
import {
  allCommands,
  allProviders,
  type CommandContext,
  commandItem,
  onCommandsChanged,
  type PaletteItem,
  type PaletteView,
  type PaletteViewProps,
} from './registry'
import { closePalette, palette, recents, rememberItem } from './state'
import './palette.css'

interface Frame {
  view: PaletteView | null
  query: string
}

function useRegistryVersion(): number {
  const [v, setV] = useState(0)
  useEffect(() => onCommandsChanged(() => setV((x) => x + 1)), [])
  return v
}

/** Run each provider for the query with debounce + abort; returns results by id. */
function useProviders(query: string, active: boolean, version: number): Record<string, ProviderResult> {
  const [results, setResults] = useState<Record<string, ProviderResult>>({})
  const ctrls = useRef(new Map<string, AbortController>())
  const timers = useRef(new Map<string, number>())
  useEffect(() => {
    if (!active) return
    const q = query.trim()
    for (const p of allProviders()) {
      ctrls.current.get(p.id)?.abort()
      window.clearTimeout(timers.current.get(p.id))
      if (q.length < (p.minQuery ?? 1)) {
        setResults((r) => (r[p.id] ? { ...r, [p.id]: { items: [], loading: false } } : r))
        continue
      }
      const ctrl = new AbortController()
      ctrls.current.set(p.id, ctrl)
      const exec = () => {
        let out: PaletteItem[] | Promise<PaletteItem[]>
        try {
          out = p.search(q, ctrl.signal)
        } catch {
          out = []
        }
        if (Array.isArray(out)) {
          setResults((r) => ({ ...r, [p.id]: { items: out as PaletteItem[], loading: false } }))
          return
        }
        setResults((r) => ({ ...r, [p.id]: { items: r[p.id]?.items ?? [], loading: true } }))
        out.then(
          (items) => {
            if (!ctrl.signal.aborted) setResults((r) => ({ ...r, [p.id]: { items, loading: false } }))
          },
          () => {
            if (!ctrl.signal.aborted) setResults((r) => ({ ...r, [p.id]: { items: [], loading: false } }))
          },
        )
      }
      const d = p.debounce ?? 80
      if (d === 0) exec()
      else {
        setResults((r) => ({ ...r, [p.id]: { items: r[p.id]?.items ?? [], loading: true } }))
        timers.current.set(p.id, window.setTimeout(exec, d))
      }
    }
  }, [query, active, version])
  useEffect(
    () => () => {
      for (const c of ctrls.current.values()) c.abort()
      for (const t of timers.current.values()) window.clearTimeout(t)
    },
    [],
  )
  return results
}

/** Items for a pushed list view (debounced when async). */
function useViewItems(view: PaletteView | null, query: string): { items: PaletteItem[]; loading: boolean } {
  const [state, setState] = useState<{ items: PaletteItem[]; loading: boolean }>({ items: [], loading: false })
  useEffect(() => {
    if (!view?.items) return
    const ctrl = new AbortController()
    let out: PaletteItem[] | Promise<PaletteItem[]>
    try {
      out = view.items(query, ctrl.signal)
    } catch {
      out = []
    }
    if (Array.isArray(out)) setState({ items: out, loading: false })
    else {
      setState((s) => ({ items: s.items, loading: true }))
      out.then(
        (items) => !ctrl.signal.aborted && setState({ items, loading: false }),
        () => !ctrl.signal.aborted && setState({ items: [], loading: false }),
      )
    }
    return () => ctrl.abort()
  }, [view, query])
  return state
}

function LazyView({ view, props }: { view: PaletteView; props: PaletteViewProps }) {
  const [C, setC] = useState<ComponentType<PaletteViewProps> | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let live = true
    setC(null)
    view.component?.().then(
      (m) => live && setC(() => m.default),
      () => live && setFailed(true),
    )
    return () => {
      live = false
    }
  }, [view])
  if (failed) return <div class="pal-empty">Couldn’t load this view. Check the connection and try again.</div>
  if (!C) return <PaletteList sections={[]} loading="Loading…" />
  return <C {...props} />
}

let seq = 0

export default function Palette() {
  const req = palette.value
  const touch = useMedia('(max-width: 767px)')
  const [stack, setStack] = useState<Frame[]>(() => [
    { view: null, query: req?.query ?? '' },
    ...(req?.view ? [{ view: req.view, query: '' }] : []),
  ])
  const top = stack[stack.length - 1]
  const query = top.query
  const [args, setArgs] = useState<Record<string, string>>({})
  const [argError, setArgError] = useState<string | null>(null)
  const [selected, setSelected] = useState<{ item: PaletteItem | null; optionId?: string }>({ item: null })
  const [panelFor, setPanelFor] = useState<PaletteItem | null>(null)
  const listId = useMemo(() => `pal${++seq}`, [])
  const controllers = useRef<{ list: ListController | null; panel: ListController | null }>({ list: null, panel: null })
  const input = useRef<HTMLInputElement>(null)
  const argRefs = useRef<Array<HTMLInputElement | null>>([])
  const root = useRef<HTMLDivElement>(null)
  const version = useRegistryVersion()
  const recentList = useComputed(() => recents.value).value

  useModal(true)
  useFocusTrap(root, true)

  const setQuery = (q: string) => setStack((s) => [...s.slice(0, -1), { ...s[s.length - 1], query: q }])
  const push = useCallback((view: PaletteView) => {
    setPanelFor(null)
    setStack((s) => [...s, { view, query: '' }])
    requestAnimationFrame(() => input.current?.focus())
  }, [])
  const pop = useCallback(() => {
    setPanelFor(null)
    setStack((s) => (s.length > 1 ? s.slice(0, -1) : s))
    requestAnimationFrame(() => input.current?.focus())
  }, [])

  const ctx: CommandContext = {
    navigate: (path) => {
      closePalette()
      navigate(path)
    },
    close: closePalette,
    push,
    pop,
    toast: (message, kind) => void toast(message, { kind }),
    copy: async (text, what = 'Text') => {
      try {
        await navigator.clipboard.writeText(text)
        toast(`${what} copied`, { kind: 'success', duration: 2200 })
      } catch {
        toast('Couldn’t copy. The browser blocked clipboard access.', { kind: 'warning' })
      }
    },
    args,
    query,
  }

  // ---------------------------------------------------------------- data
  const results = useProviders(query, top.view === null, version)
  const viewItems = useViewItems(top.view, query)
  // biome-ignore lint/correctness/useExhaustiveDependencies: version tracks registry changes
  const commands = useMemo(() => allCommands().map(commandItem), [version])

  let sections: Section[] = []
  let loading: string | false = false
  if (top.view === null) {
    const composed = composeRoot({ query, commands, providers: allProviders(), results, recents: recentList })
    sections = composed.sections
    loading = composed.loading
  } else if (top.view.items) {
    const ranked = top.view.filter === false ? viewItems.items.map((item) => ({ item, score: 0 })) : rankItems(query, viewItems.items)
    sections = groupRanked(ranked, { fallback: top.view.title })
    loading = viewItems.loading && !viewItems.items.length ? 'Loading…' : false
  }
  const noResults = top.view === null && query.trim() !== '' && !sections.length && !loading

  // Reset inline args when the selection changes.
  const selId = selected.item?.id
  // biome-ignore lint/correctness/useExhaustiveDependencies: keyed on the selected id
  useEffect(() => {
    setArgs({})
    setArgError(null)
  }, [selId])

  // ---------------------------------------------------------------- running
  const runItem = (item: PaletteItem, actionIndex?: number) => {
    if (actionIndex !== undefined) {
      const a = item.actions?.[actionIndex]
      if (!a) return
      setPanelFor(null)
      rememberItem(item)
      void Promise.resolve(a.run({ ...ctx, args })).catch((e) => toast(errorText(e), { kind: 'danger' }))
      return
    }
    if (item.args?.length && item.id === selected.item?.id) {
      const missing = item.args.findIndex((a) => !a.optional && !args[a.name]?.trim())
      if (missing >= 0) {
        setArgError(item.args[missing].name)
        argRefs.current[missing]?.focus()
        return
      }
    } else if (item.args?.some((a) => !a.optional)) {
      // Tapped a row with required args that was not selected: it is now; ask for them.
      requestAnimationFrame(() => argRefs.current[0]?.focus())
      return
    }
    if (item.view) {
      push(item.view)
      return
    }
    rememberItem(item)
    if (item.run) {
      void Promise.resolve(item.run({ ...ctx, args })).catch((e) => toast(errorText(e), { kind: 'danger' }))
      return
    }
    if (item.link) ctx.navigate(item.link)
  }

  const api = {
    ctx,
    runItem,
    openActions: (item: PaletteItem) => setPanelFor(item),
    setController: (slot: 'list' | 'panel', c: ListController | null) => {
      controllers.current[slot] = c
    },
    onSelect: (item: PaletteItem | null, optionId?: string) => setSelected({ item, optionId }),
    touch,
    listId,
  }

  // ---------------------------------------------------------------- keys
  const onKeyDown = (e: KeyboardEvent) => {
    const mod = isMac ? e.metaKey : e.ctrlKey
    const target = e.target as HTMLInputElement
    const inMain = target === input.current
    const c = panelFor ? controllers.current.panel : controllers.current.list
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowUp':
        e.preventDefault()
        c?.move(e.key === 'ArrowDown' ? 1 : -1)
        return
      case 'PageDown':
      case 'PageUp':
        e.preventDefault()
        c?.move(e.key === 'PageDown' ? 8 : -8)
        return
      case 'Home':
      case 'End':
        if (!mod) return
        e.preventDefault()
        c?.edge(e.key === 'Home' ? 'first' : 'last')
        return
      case 'Enter':
        if (e.isComposing) return
        e.preventDefault()
        if (!c?.run(mod && !panelFor) && mod) c?.run(false)
        return
      case 'Escape':
        e.preventDefault()
        e.stopPropagation()
        if (panelFor) setPanelFor(null)
        else if (stack.length > 1) pop()
        else if (query) setQuery('')
        else closePalette()
        return
      case 'Backspace':
        if (inMain && !query && stack.length > 1 && !panelFor) {
          e.preventDefault()
          pop()
        }
        return
      case 'ArrowRight':
        if (inMain && !panelFor && target.selectionStart === query.length && selected.item?.actions?.length) {
          e.preventDefault()
          setPanelFor(selected.item)
        }
        return
      case 'ArrowLeft':
        if (panelFor) {
          e.preventDefault()
          setPanelFor(null)
        }
        return
      case 'Tab':
        if (selected.item?.args?.length && !e.shiftKey && inMain) {
          e.preventDefault()
          argRefs.current[0]?.focus()
        }
        return
    }
    if (mod && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      if (panelFor) setPanelFor(null)
      else if (selected.item) setPanelFor(selected.item)
    }
  }

  const title = top.view?.title
  const placeholder = top.view?.placeholder ?? (top.view ? `Search ${top.view.title.toLowerCase()}…` : 'Search or run a command…')
  const argsFor = selected.item?.args?.length && !panelFor ? selected.item : null
  const primaryLabel = selected.item ? (selected.item.view ? 'Open' : (selected.item.runLabel ?? (selected.item.link ? 'Open' : 'Run'))) : null

  const panelSections: Section[] = panelFor
    ? [
        {
          title: panelFor.title,
          items: [
            { item: { id: `${panelFor.id}#primary`, title: primaryLabel ?? 'Open', icon: 'corner-down-left', shortcut: ['enter'] }, score: 0 },
            ...(panelFor.actions ?? []).map((a, i) => ({
              item: { id: `${panelFor.id}#${a.id}`, title: a.title, icon: a.icon, shortcut: a.shortcut ?? (i === 0 ? ['mod', 'enter'] : undefined) },
              score: 0,
            })),
          ],
        },
      ]
    : []

  return (
    <Portal>
      <PaletteContext.Provider value={api}>
        <div class="pal-layer" data-touch={touch || undefined}>
          <div class="scrim scrim--in pal-scrim" onClick={closePalette} aria-hidden="true" />
          <div
            ref={root}
            class="pal"
            role="dialog"
            aria-modal="true"
            aria-label="Command center"
            data-modal
            onKeyDown={onKeyDown}
          >
            <div class="pal-search">
              {stack.length > 1 ? (
                <button type="button" class="pal-back" onClick={pop} aria-label="Back">
                  <Icon name="chevron-left" size={18} />
                </button>
              ) : (
                <span class="pal-search__icon" aria-hidden="true">
                  <Icon name="search" size={18} />
                </span>
              )}
              {title && <span class="pal-crumb">{title}</span>}
              <input
                ref={input}
                data-autofocus
                class="pal-input"
                type="text"
                role="combobox"
                aria-expanded="true"
                aria-controls={listId}
                aria-activedescendant={panelFor ? undefined : selected.optionId}
                aria-autocomplete="list"
                aria-label={title ? `Search ${title}` : 'Search commands, sessions, files'}
                placeholder={argsFor ? argsFor.title : placeholder}
                value={query}
                autoComplete="off"
                autoCapitalize="off"
                autoCorrect="off"
                spellcheck={false}
                enterKeyHint="go"
                onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
              />
              {argsFor && (
                <div class="pal-args" role="group" aria-label={`${argsFor.title} arguments`}>
                  {argsFor.args?.map((a, i) => (
                    <input
                      key={`${argsFor.id}:${a.name}`}
                      ref={(el) => {
                        argRefs.current[i] = el
                      }}
                      class={`pal-arg${argError === a.name ? ' is-invalid' : ''}`}
                      placeholder={a.optional ? `${a.placeholder} (optional)` : a.placeholder}
                      aria-label={a.placeholder}
                      aria-invalid={argError === a.name || undefined}
                      value={args[a.name] ?? ''}
                      size={Math.max(8, Math.min(24, (args[a.name] ?? a.placeholder).length + 1))}
                      onInput={(e) => {
                        setArgError(null)
                        setArgs((s) => ({ ...s, [a.name]: (e.target as HTMLInputElement).value }))
                      }}
                    />
                  ))}
                </div>
              )}
              {touch ? (
                <button type="button" class="pal-cancel" onClick={closePalette}>
                  Cancel
                </button>
              ) : (
                <Kbd keys={['esc']} class="pal-esc" />
              )}
            </div>

            <div class="pal-body">
              {top.view?.component ? (
                <LazyView view={top.view} props={{ ctx, query, pop }} />
              ) : (
                <PaletteList
                  sections={noResults ? groupRanked(noResultItems(query).map((item) => ({ item, score: 0 }))) : sections}
                  loading={loading}
                  resetKey={`${stack.length}:${query}`}
                  label={title ?? 'Results'}
                  empty={<PaletteEmpty view={top.view} />}
                />
              )}
              {noResults && (
                <p class="pal-noresults" role="status">
                  Nothing matches “{query.trim()}”.
                </p>
              )}
            </div>

            {panelFor && (
              <div class="pal-panel" role="dialog" aria-label={`Actions for ${panelFor.title}`}>
                <PaletteList
                  slot="panel"
                  label={`Actions for ${panelFor.title}`}
                  sections={panelSections}
                  onRun={(it) => {
                    const idx = (panelFor.actions ?? []).findIndex((a) => it.id === `${panelFor.id}#${a.id}`)
                    if (idx >= 0) runItem(panelFor, idx)
                    else {
                      setPanelFor(null)
                      runItem(panelFor)
                    }
                  }}
                />
                {touch && (
                  <button type="button" class="pal-panel__close" onClick={() => setPanelFor(null)}>
                    Close
                  </button>
                )}
              </div>
            )}

            {!touch && (
              <div class="pal-foot">
                <span class="pal-foot__where">
                  <Icon name="glyph:relay" size={14} />
                  {title ?? 'Relay'}
                </span>
                <span class="pal-foot__keys">
                  {primaryLabel && (
                    <span class="pal-foot__key">
                      {primaryLabel} <Kbd keys={['enter']} />
                    </span>
                  )}
                  {selected.item?.actions?.length ? (
                    <>
                      <span class="pal-foot__sep" aria-hidden="true" />
                      <span class="pal-foot__key">
                        Actions <Kbd keys={['mod', 'K']} />
                      </span>
                    </>
                  ) : null}
                </span>
              </div>
            )}
          </div>
        </div>
      </PaletteContext.Provider>
    </Portal>
  )
}

function PaletteEmpty({ view }: { view: PaletteView | null }) {
  return <div class="pal-empty">{view ? `Nothing in ${view.title.toLowerCase()} yet.` : 'Type to search commands, sessions and files.'}</div>
}

function errorText(e: unknown): string {
  return e instanceof Error && e.message ? e.message : 'That didn’t work. Try again.'
}
