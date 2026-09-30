/**
 * InstallOneLiner behaviour: copy the command, show "Copied" feedback,
 * announce it to screen readers, and fall back to selecting the text when
 * the Clipboard API is unavailable (http, old browsers).
 */
import type { Island } from '../lib/runtime'

/** Copy text; resolves true on success. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.append(ta)
    ta.select()
    let ok = false
    try {
      ok = document.execCommand('copy')
    } catch {
      ok = false
    }
    ta.remove()
    return ok
  }
}

const copy: Island = (root) => {
  const buttons = [...root.querySelectorAll<HTMLButtonElement>('[data-copy]')]
  const status = root.querySelector<HTMLElement>('[data-copy-status]')
  const timers = new Map<HTMLButtonElement, number>()
  const handlers = buttons.map((btn) => {
    const label = btn.querySelector<HTMLElement>('[data-copy-label]')
    const idle = label?.textContent ?? ''
    const onClick = async () => {
      const text = btn.dataset.copy || ''
      const ok = await copyText(text)
      const msg = ok ? 'Copied' : 'Press Ctrl+C to copy'
      if (!ok) {
        const code = root.querySelector('code')
        if (code) window.getSelection()?.selectAllChildren(code)
      }
      if (label) label.textContent = msg
      btn.dataset.state = ok ? 'copied' : 'failed'
      if (status) status.textContent = ok ? 'Install command copied to the clipboard.' : 'Copy failed; the command is selected.'
      clearTimeout(timers.get(btn))
      timers.set(
        btn,
        window.setTimeout(() => {
          if (label) label.textContent = idle
          delete btn.dataset.state
        }, 1800),
      )
    }
    btn.addEventListener('click', onClick)
    return () => btn.removeEventListener('click', onClick)
  })
  return () => {
    for (const off of handlers) off()
    for (const t of timers.values()) clearTimeout(t)
  }
}

export default copy
