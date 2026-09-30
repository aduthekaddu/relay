import { useEffect, useRef, useState } from 'preact/hooks'
import { cx } from '../lib/util'
import { Skeleton } from './layout'
import './code.css'

export interface MarkdownProps {
  /** Markdown source (untrusted: it is sanitised). */
  source: string
  /** Denser type for transcripts and previews. */
  compact?: boolean
  class?: string
}

/**
 * Rendered, sanitised Markdown with highlighted fenced code. marked,
 * DOMPurify and highlight.js load on first use.
 */
export function Markdown({ source, compact, class: className }: MarkdownProps) {
  const [html, setHtml] = useState<string | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    let live = true
    import('./markdown-impl').then((m) => live && setHtml(m.renderMarkdown(source)))
    return () => {
      live = false
    }
  }, [source])
  useEffect(() => {
    const root = ref.current
    if (!root || html === null) return
    const blocks = root.querySelectorAll<HTMLElement>('pre > code')
    if (blocks.length === 0) return
    let live = true
    import('./highlight').then(async (h) => {
      for (const el of blocks) {
        const lang = /language-([\w+-]+)/.exec(el.className)?.[1]
        const out = await h.highlight(el.textContent ?? '', lang)
        if (!live) return
        if (out !== null) {
          el.innerHTML = out
          el.classList.add('hljs')
        }
      }
    })
    return () => {
      live = false
    }
  }, [html])
  if (html === null) return <Skeleton lines={3} class={className} />
  // biome-ignore lint/security/noDangerouslySetInnerHtml: sanitised by DOMPurify in markdown-impl
  return (
    <div
      ref={ref}
      class={cx('md', compact && 'md--compact', className)}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}
