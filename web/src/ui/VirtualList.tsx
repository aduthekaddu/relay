import type { ComponentChildren, Ref } from 'preact'
import { forwardRef } from 'preact/compat'
import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState } from 'preact/hooks'
import { cx } from '../lib/util'
import './misc.css'

export interface VirtualListProps<T> {
  items: T[]
  /** Fixed row height in px (virtualisation needs it). */
  itemHeight: number
  /** Render one row. Rows are absolutely positioned; fill the height. */
  render: (item: T, index: number) => ComponentChildren
  /** Stable key per item. */
  itemKey: (item: T, index: number) => string | number
  /** Extra rows rendered above/below the viewport. Default 6. */
  overscan?: number
  /** Called when the user scrolls within `endThreshold` px of the end (load more). */
  onEndReached?: () => void
  endThreshold?: number
  /** Accessible name; the container is a list. */
  label: string
  class?: string
}

export interface VirtualListHandle {
  scrollToIndex: (index: number, align?: 'start' | 'center' | 'nearest') => void
  element: HTMLDivElement | null
}

/** Visible index window for a scroll position (exported for tests). */
export function visibleRange(
  scrollTop: number,
  viewport: number,
  itemHeight: number,
  count: number,
  overscan: number,
): [number, number] {
  if (count === 0 || itemHeight <= 0) return [0, 0]
  const first = Math.max(0, Math.floor(scrollTop / itemHeight) - overscan)
  const last = Math.min(count, Math.ceil((scrollTop + viewport) / itemHeight) + overscan)
  return [first, last]
}

/**
 * Windowed list for thousands of rows (history, processes, logs). The
 * container fills its parent's height; give the parent a height.
 */
function VirtualListInner<T>(
  {
    items,
    itemHeight,
    render,
    itemKey,
    overscan = 6,
    onEndReached,
    endThreshold = 400,
    label,
    class: className,
  }: VirtualListProps<T>,
  ref: Ref<VirtualListHandle>,
) {
  const el = useRef<HTMLDivElement>(null)
  const [top, setTop] = useState(0)
  const [vh, setVh] = useState(600)
  const endFired = useRef(false)

  useLayoutEffect(() => {
    const node = el.current
    if (!node) return
    setVh(node.clientHeight)
    const ro = new ResizeObserver(() => setVh(node.clientHeight))
    ro.observe(node)
    return () => ro.disconnect()
  }, [])

  useEffect(() => {
    endFired.current = false
  }, [items.length])

  useImperativeHandle(
    ref,
    () => ({
      element: el.current,
      scrollToIndex(index, align = 'nearest') {
        const node = el.current
        if (!node) return
        const y = index * itemHeight
        if (align === 'start') node.scrollTop = y
        else if (align === 'center') node.scrollTop = y - node.clientHeight / 2 + itemHeight / 2
        else if (y < node.scrollTop) node.scrollTop = y
        else if (y + itemHeight > node.scrollTop + node.clientHeight)
          node.scrollTop = y + itemHeight - node.clientHeight
      },
    }),
    [itemHeight],
  )

  const onScroll = () => {
    const node = el.current
    if (!node) return
    setTop(node.scrollTop)
    if (
      onEndReached &&
      !endFired.current &&
      node.scrollHeight - node.scrollTop - node.clientHeight < endThreshold
    ) {
      endFired.current = true
      onEndReached()
    }
  }

  const [first, last] = visibleRange(top, vh, itemHeight, items.length, overscan)
  const rows: ComponentChildren[] = []
  for (let i = first; i < last; i++) {
    rows.push(
      // biome-ignore lint/a11y/useSemanticElements: absolutely positioned rows inside a scroll spacer
      <div
        key={itemKey(items[i], i)}
        class="vlist__row"
        role="listitem"
        aria-setsize={items.length}
        aria-posinset={i + 1}
        style={{ transform: `translateY(${i * itemHeight}px)`, height: itemHeight }}
      >
        {render(items[i], i)}
      </div>,
    )
  }
  return (
    // biome-ignore lint/a11y/useSemanticElements: virtualised list needs a div scroller
    <div
      ref={el}
      class={cx('vlist', className)}
      onScroll={onScroll}
      role="list"
      aria-label={label}
      // biome-ignore lint/a11y/noNoninteractiveTabindex: scrollable regions must be keyboard focusable
      tabIndex={0}
    >
      <div class="vlist__spacer" style={{ height: items.length * itemHeight }}>
        {rows}
      </div>
    </div>
  )
}

export const VirtualList = forwardRef(VirtualListInner) as <T>(
  p: VirtualListProps<T> & { ref?: Ref<VirtualListHandle> },
) => ReturnType<typeof VirtualListInner>
