// Page chrome: what the shell header shows for the current screen.
//
// Area screens call usePageChrome({ title, actions }) to set the header
// title (defaults to the area name), a quiet subtitle in the instrument
// layer, contextual header actions and an optional back link. Screens that
// need to go full-bleed at runtime (e.g. a file editor) call
// useImmersive(true); routes marked `immersive` in routes.ts are immersive
// automatically.
import type { ComponentChildren } from 'preact'
import { signal } from '@preact/signals'
import { useLayoutEffect } from 'preact/hooks'

export interface PageChrome {
  /** Header title; defaults to the area label. */
  title?: string
  /** Mono meta line under/after the title (path, count, host). */
  subtitle?: string
  /** Buttons rendered at the right of the header (before search/bell). */
  actions?: ComponentChildren
  /** In-app path for a back affordance (mobile header shows ‹). */
  back?: string
}

/** Current chrome, set by the screen on top. */
export const pageChrome = signal<PageChrome>({})
/** Runtime immersive override (null = use the route's flag). */
export const immersiveOverride = signal<boolean | null>(null)

/**
 * Set the header for this screen while it is mounted. Pass the values
 * that change as `deps` (defaults to title + subtitle + back).
 */
export function usePageChrome(c: PageChrome, deps: unknown[] = [c.title, c.subtitle, c.back]): void {
  // biome-ignore lint/correctness/useExhaustiveDependencies: caller controls deps
  useLayoutEffect(() => {
    pageChrome.value = c
    return () => {
      if (pageChrome.value === c) pageChrome.value = {}
    }
  }, deps)
}

/** Force immersive mode on or off while mounted. */
export function useImmersive(on: boolean): void {
  useLayoutEffect(() => {
    immersiveOverride.value = on
    return () => {
      immersiveOverride.value = null
    }
  }, [on])
}
