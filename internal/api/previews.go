package api

// PreviewLink answers GET /api/v1/previews/{port}/link (used by
// `relay preview <port>`): the URL to open for a port, whether anything
// is listening on it yet, and the detected preview when it is.
type PreviewLink struct {
	Port      int      `json:"port"`
	URL       string   `json:"url"`
	Mode      string   `json:"mode"` // "subdomain" | "path"
	Listening bool     `json:"listening"`
	Preview   *Preview `json:"preview,omitempty"`
}
