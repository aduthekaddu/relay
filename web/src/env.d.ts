/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "1" enables the in-browser mock backend (pnpm dev:mock). */
  readonly VITE_MOCK?: string
}
