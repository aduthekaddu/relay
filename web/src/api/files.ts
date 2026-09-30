// Mirror of internal/api/files.go — additional contract types for the files
// feature (see docs/dev/FILES_SYSTEM.md). Keep field names in sync.

/** GET /api/v1/files/text */
export interface TextFile {
  path: string
  text: string
  size: number
  modTime: string
  truncated: boolean
  /** "binary" means text is empty: offer a download instead. */
  encoding: 'utf-8' | 'utf-8-bom' | 'binary'
}

/** PUT /api/v1/files/text?path=&mtime= (409 when the file changed since mtime). */
export interface SaveTextRequest {
  text: string
}

/** POST /api/v1/open, and the payload of the "open" event. */
export interface OpenRequest {
  path: string
  line?: number
}

export type FileJobState = 'running' | 'done' | 'failed' | 'canceled'

/** Background copy job: 202 body of POST /api/v1/files/copy and payload of "files.job". */
export interface FileJob {
  id: string
  op: 'copy'
  state: FileJobState
  from: string[]
  to: string
  files: number
  totalFiles: number
  bytes: number
  totalBytes: number
  current?: string
  skipped: number
  error?: string
  result?: string[]
  startedAt: string
  endedAt?: string
}

export const EvFilesJob = 'files.job'
