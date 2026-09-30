package ptyd

import "github.com/aduthekaddu/relay/internal/httpx"

// Service errors carry their HTTP status (see httpx.Fail).
func badRequest(msg string) error  { return httpx.BadRequest(msg) }
func notFound(msg string) error    { return httpx.NotFound(msg) }
func conflict(msg string) error    { return httpx.Conflict(msg) }
func unavailable(msg string) error { return httpx.Unavailable(msg) }
