package api

import "time"

// Additional contract types for the files feature (see docs/dev/API.md
// "files" and docs/dev/FILES_SYSTEM.md). Mirrored in web/src/api/files.ts.

// TextFile is the response of GET /api/v1/files/text.
type TextFile struct {
	Path      string    `json:"path"`
	Text      string    `json:"text"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"modTime"`
	Truncated bool      `json:"truncated"`
	// Encoding is "utf-8", "utf-8-bom" (BOM stripped from Text and kept on
	// save) or "binary" (Text is empty; offer a download instead).
	Encoding string `json:"encoding"`
}

// SaveTextRequest is the body of PUT /api/v1/files/text.
type SaveTextRequest struct {
	Text string `json:"text"`
}

// OpenRequest is the body of POST /api/v1/open and the payload of EvOpen.
type OpenRequest struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// FileJob describes a background file operation (copy). It is returned by
// POST /api/v1/files/copy (202) and published as EvFilesJob on progress.
type FileJob struct {
	ID         string    `json:"id"`
	Op         string    `json:"op"`    // copy
	State      string    `json:"state"` // running | done | failed | canceled
	From       []string  `json:"from"`
	To         string    `json:"to"`
	Files      int64     `json:"files"`      // files copied so far
	TotalFiles int64     `json:"totalFiles"` // files to copy (after the size scan)
	Bytes      int64     `json:"bytes"`
	TotalBytes int64     `json:"totalBytes"`
	Current    string    `json:"current,omitempty"` // path being copied
	Skipped    int64     `json:"skipped"`           // unreadable or special files left out
	Error      string    `json:"error,omitempty"`
	Result     []string  `json:"result,omitempty"` // top-level destination paths
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt,omitempty"`
}

// EvFilesJob is published while a FileJob runs (at most ~4 per second)
// and once when it ends.
const EvFilesJob = "files.job" // (FileJob)
