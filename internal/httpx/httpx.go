// Package httpx holds small HTTP helpers shared by every feature package.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
)

// MaxJSONBody caps request bodies decoded with Decode.
const MaxJSONBody = 1 << 20 // 1 MiB

// JSON writes v with status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// OK writes 200 with v.
func OK(w http.ResponseWriter, v any) { JSON(w, http.StatusOK, v) }

// NoContent writes 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Error writes the standard error envelope.
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, api.ErrorBody{Error: api.ErrorDetail{Code: code, Message: message}})
}

// Err is an error that carries an HTTP status and code. Feature packages
// return these from their services; handlers pass them to Fail.
type Err struct {
	Status  int
	Code    string
	Message string
	Field   string
	RetryIn int
}

func (e *Err) Error() string { return e.Message }

func BadRequest(msg string) *Err   { return &Err{Status: 400, Code: "bad_request", Message: msg} }
func NotFound(msg string) *Err     { return &Err{Status: 404, Code: "not_found", Message: msg} }
func Forbidden(msg string) *Err    { return &Err{Status: 403, Code: "forbidden", Message: msg} }
func Conflict(msg string) *Err     { return &Err{Status: 409, Code: "conflict", Message: msg} }
func Unavailable(msg string) *Err  { return &Err{Status: 503, Code: "unavailable", Message: msg} }
func Unauthorized(msg string) *Err { return &Err{Status: 401, Code: "unauthorized", Message: msg} }

// Fail writes err. *Err values keep their status; anything else is a 500
// whose message is not leaked to the client.
func Fail(w http.ResponseWriter, err error) {
	var e *Err
	if errors.As(err, &e) {
		if e.RetryIn > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(e.RetryIn))
		}
		JSON(w, e.Status, api.ErrorBody{Error: api.ErrorDetail{Code: e.Code, Message: e.Message, Field: e.Field, RetryIn: e.RetryIn}})
		return
	}
	Error(w, http.StatusInternalServerError, "internal", "something went wrong")
}

// Decode reads a JSON body into v (unknown fields rejected, size capped).
func Decode(r *http.Request, v any) error {
	return DecodeLimit(r, v, MaxJSONBody)
}

func DecodeLimit(r *http.Request, v any, limit int64) error {
	if r.Body == nil {
		return BadRequest("missing body")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return BadRequest("empty body")
		}
		return BadRequest("invalid JSON: " + err.Error())
	}
	return nil
}

// QueryInt parses an integer query parameter with bounds.
func QueryInt(r *http.Request, name string, def, min, max int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// QueryBool treats "1", "true", "yes" as true.
func QueryBool(r *http.Request, name string) bool {
	switch strings.ToLower(r.URL.Query().Get(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ClientIP returns the peer address. X-Forwarded-For is honoured only when
// the immediate peer is trusted (see server.TrustedProxies).
func ClientIP(r *http.Request, trusted func(net.IP) bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer != nil && trusted != nil && trusted(peer) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				ip := net.ParseIP(strings.TrimSpace(parts[i]))
				if ip != nil && !trusted(ip) {
					return ip.String()
				}
			}
		}
		if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
			return cf
		}
	}
	if host == "" {
		return "local"
	}
	return host
}

// IsUnsafeMethod reports whether the method can change state.
func IsUnsafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}
