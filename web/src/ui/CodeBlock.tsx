import { useEffect, useMemo, useState } from 'preact/hooks'
import { cx } from '../lib/util'
import { Icon } from './Icon'
import { toast } from './Toast'
import './code.css'

export interface CodeBlockProps {
  code: string
  /** Language id, alias or extension ("ts", "go", "diff"…). */
  lang?: string
  /** Optional file name shown in the header. */
  filename?: string
  lineNumbers?: boolean
  /** Soft-wrap long lines (default false: horizontal scroll). */
  wrap?: boolean
  /** Max height before the block scrolls (CSS length). */
  maxHeight?: string
  /** Show a copy button (default true). */
  copy?: boolean
  class?: string
}

/**
 * Syntax-highlighted code. Renders plain text instantly (no layout shift)
 * and swaps in highlighting once highlight.js and the language load.
 */
export function CodeBlock({
  code,
  lang,
  filename,
  lineNumbers = false,
  wrap = false,
  maxHeight,
  copy = true,
  class: className,
}: CodeBlockProps) {
  const [html, setHtml] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    setHtml(null)
    import('./highlight')
      .then((m) => m.highlight(code, lang))
      .then((h) => live && setHtml(h))
      .catch(() => {
        /* highlighting is optional; keep plain text */
      })
    return () => {
      live = false
    }
  }, [code, lang])
  const lines = useMemo(() => (lineNumbers ? code.split('\n').length : 0), [code, lineNumbers])
  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      toast('Copied', { kind: 'success', duration: 1600 })
    } catch {
      toast("Couldn't copy — select the text instead", { kind: 'warning' })
    }
  }
  return (
    <figure class={cx('code', wrap && 'code--wrap', className)}>
      {(filename || lang || copy) && (
        <figcaption class="code__head">
          <span class="code__name t-mono">{filename ?? lang ?? ''}</span>
          {copy && (
            <button type="button" class="code__copy" onClick={onCopy} aria-label="Copy code">
              <Icon name="copy" size={14} />
            </button>
          )}
        </figcaption>
      )}
      <div class="code__scroll" style={maxHeight ? { maxHeight } : undefined}>
        {lineNumbers && (
          <span class="code__gutter" aria-hidden="true">
            {Array.from({ length: lines }, (_, i) => `${i + 1}\n`).join('')}
          </span>
        )}
        {html !== null ? (
          // biome-ignore lint/security/noDangerouslySetInnerHtml: highlight.js output escapes the source text
          <pre class="code__pre hljs" dangerouslySetInnerHTML={{ __html: html }} />
        ) : (
          <pre class="code__pre">{code}</pre>
        )}
      </div>
    </figure>
  )
}
