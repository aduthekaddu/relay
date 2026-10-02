package system

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/files"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

const (
	logWriteTimeout = 10 * time.Second
	logPingInterval = 25 * time.Second
	logMaxLines     = 2000
)

// logSource is a validated request for a log stream.
type logSource struct {
	unit  string // systemd user unit, or
	file  string // resolved regular file inside home
	lines int
}

// parseLogRequest validates ?unit= / ?file= / ?lines= before upgrading.
func (s *Service) parseLogRequest(r *http.Request) (logSource, error) {
	q := r.URL.Query()
	src := logSource{lines: httpx.QueryInt(r, "lines", 200, 0, logMaxLines)}
	unit, file := q.Get("unit"), q.Get("file")
	switch {
	case unit != "" && file != "":
		return src, httpx.BadRequest("pass either unit or file, not both")
	case unit != "":
		name, err := NormalizeUnit(unit)
		if err != nil {
			return src, err
		}
		if s.journalctl == "" {
			return src, httpx.Unavailable("journalctl is not available")
		}
		src.unit = name
	case file != "":
		res, err := files.NewResolver(s.home, s.home)
		if err != nil {
			return src, err
		}
		real, err := res.Resolve(file)
		if err != nil {
			return src, err
		}
		fi, err := os.Stat(real)
		if err != nil {
			return src, httpx.NotFound("no such file")
		}
		if !fi.Mode().IsRegular() {
			return src, httpx.BadRequest("logs can only follow regular files")
		}
		src.file = real
	default:
		return src, &httpx.Err{Status: 400, Code: "bad_request", Message: "unit or file is required", Field: "unit"}
	}
	return src, nil
}

// handleLogs streams api.LogLine JSON text frames until the client goes
// away. Client frames are ignored (pause is client-side).
func (s *Service) handleLogs(w http.ResponseWriter, r *http.Request) {
	src, err := s.parseLogRequest(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	select {
	case s.logSem <- struct{}{}:
		defer func() { <-s.logSem }()
	default:
		httpx.Fail(w, &httpx.Err{Status: 429, Code: "rate_limited", Message: "too many log streams open"})
		return
	}
	conn, guard, err := server.AcceptSocket(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return // Accept wrote the error response
	}
	defer guard.Stop()
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(4 << 10)
	ctx := conn.CloseRead(r.Context()) // cancelled when the client closes

	go func() { // keepalive through proxies
		t := time.NewTicker(logPingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, cancel := context.WithTimeout(ctx, logWriteTimeout)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	sink := func(ll api.LogLine) error {
		b, err := json.Marshal(ll)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, logWriteTimeout)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, b)
	}
	if src.unit != "" {
		err = journalFollow(ctx, s.journalctl, src.unit, src.lines, sink)
	} else {
		err = tailFile(ctx, src.file, src.lines, sink)
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		s.log.Debug("log stream ended", "err", err)
		_ = conn.Close(websocket.StatusInternalError, "log source ended")
		return
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}
