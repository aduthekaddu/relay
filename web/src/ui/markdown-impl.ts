// Markdown → sanitised HTML. Loaded lazily by ui/Markdown.tsx; never import
// statically from the shell.
import DOMPurify from 'dompurify'
import { Marked } from 'marked'

const md = new Marked({ gfm: true, breaks: false, async: false })

let hooked = false
function hook() {
  if (hooked) return
  hooked = true
  // External links open in a new tab without access to window.opener.
  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName === 'A') {
      const href = node.getAttribute('href') ?? ''
      if (/^https?:\/\//i.test(href)) {
        node.setAttribute('target', '_blank')
        node.setAttribute('rel', 'noopener noreferrer')
      }
    }
    if (node.tagName === 'IMG') {
      node.setAttribute('loading', 'lazy')
      node.setAttribute('decoding', 'async')
    }
  })
}

/**
 * Render untrusted Markdown to safe HTML: no scripts, no event handlers, no
 * inline styles, no iframes/forms; only http(s), mailto, relative and data:
 * image URLs survive.
 */
export function renderMarkdown(src: string): string {
  hook()
  const raw = md.parse(src.length > 500_000 ? `${src.slice(0, 500_000)}\n\n…` : src) as string
  return DOMPurify.sanitize(raw, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'iframe', 'form', 'input', 'button', 'textarea', 'select', 'object', 'embed'],
    FORBID_ATTR: ['style'],
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[^a-z]|[a-z+.-]+(?:[^a-z+.\-:]|$))/i,
  })
}
