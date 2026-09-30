// Mirror of internal/api/previews.go — keep in sync (same field names).
import type { Preview } from './types'

/** GET /api/v1/previews/{port}/link — URL to open for a port (used by `relay preview`). */
export interface PreviewLink {
  port: number
  url: string
  mode: 'subdomain' | 'path'
  listening: boolean
  preview?: Preview
}
