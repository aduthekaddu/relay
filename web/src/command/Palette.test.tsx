import { act, cleanup, fireEvent, render, screen } from '@testing-library/preact'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Palette from './Palette'
import { type Command, registerCommands, unregisterCommand } from './registry'
import { closePalette, openPalette, palette } from './state'

const flush = () => act(() => new Promise<void>((r) => setTimeout(r, 0)))

function Host() {
  return palette.value ? <Palette /> : null
}

describe('Palette', () => {
  // Commands close the palette themselves (ctx.close) so that some, like
  // copy-to-clipboard variants, can keep it open.
  const runA = vi.fn((ctx: { close: () => void }) => ctx.close())
  const runB = vi.fn((ctx: { close: () => void }) => ctx.close())
  const runSub = vi.fn()
  const cmds: Command[] = [
    { id: 't.alpha', title: 'Alpha tool', section: 'Test', run: runA, suggested: true },
    { id: 't.beta', title: 'Beta tool', section: 'Test', run: runB },
    {
      id: 't.pick',
      title: 'Pick a colour',
      section: 'Test',
      view: {
        id: 'colours',
        title: 'Colours',
        items: () => [
          { id: 'c.red', title: 'Red', run: runSub },
          { id: 'c.blue', title: 'Blue', run: runSub },
        ],
      },
    },
  ]
  beforeEach(() => {
    localStorage.clear()
    registerCommands(cmds)
  })
  afterEach(() => {
    closePalette()
    cleanup()
    for (const c of cmds) unregisterCommand(c.id)
    vi.clearAllMocks()
  })

  const field = () => screen.getByRole('combobox') as HTMLInputElement
  const type = async (text: string) => {
    fireEvent.input(field(), { target: { value: text } })
    await flush()
  }
  const key = async (k: string, init: KeyboardEventInit = {}) => {
    fireEvent.keyDown(field(), { key: k, ...init })
    await flush()
  }

  it('filters, moves the selection and runs with Enter', async () => {
    render(<Host />)
    act(() => openPalette())
    await flush()
    await type('tool')
    const options = screen.getAllByRole('option')
    expect(options.map((o) => o.textContent)).toEqual(
      expect.arrayContaining([expect.stringContaining('Alpha'), expect.stringContaining('Beta')]),
    )
    await key('ArrowDown')
    await key('Enter')
    expect(runA.mock.calls.length + runB.mock.calls.length).toBe(1)
    expect(palette.value).toBeNull()
  })

  it('pushes a sub-view and pops it with Escape', async () => {
    render(<Host />)
    act(() => openPalette({ query: 'pick' }))
    await flush()
    await key('Enter')
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual([
      expect.stringContaining('Red'),
      expect.stringContaining('Blue'),
    ])
    await key('Escape') // pop the view: the root query comes back
    expect(field().value).toBe('pick')
    await key('Escape') // clear the query
    expect(field().value).toBe('')
    expect(palette.value).not.toBeNull()
    await key('Escape') // close
    expect(palette.value).toBeNull()
    expect(runSub).not.toHaveBeenCalled()
  })

  it('offers fallback actions when nothing matches', async () => {
    render(<Host />)
    act(() => openPalette({ query: 'zzqqxx' }))
    await flush()
    expect(screen.getByRole('status').textContent).toContain('Nothing matches “zzqqxx”')
    for (const o of screen.getAllByRole('option')) expect(o.textContent).toContain('zzqqxx')
  })
})
