package notify

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// Routes registers the notification, settings and push endpoints.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/notifications", s.handleList)
	rt.Handle("POST /api/v1/notifications/read", s.handleRead)
	rt.Handle("DELETE /api/v1/notifications/{id}", s.handleDelete)
	rt.Handle("POST /api/v1/notify", s.handleNotify)
	rt.Handle("GET /api/v1/notify/settings", s.handleGetSettings)
	rt.Handle("PATCH /api/v1/notify/settings", s.handlePatchSettings)
	rt.Handle("GET /api/v1/push/key", s.handleKey)
	rt.Handle("POST /api/v1/push/subscribe", s.handleSubscribe)
	rt.Handle("POST /api/v1/push/unsubscribe", s.handleUnsubscribe)
	rt.Handle("POST /api/v1/push/test", s.handleTest)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	limit := httpx.QueryInt(r, "limit", 50, 1, InboxRetain)
	list, err := s.List(r.Context(), limit, httpx.QueryBool(r, "unread"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, list)
}

type readRequest struct {
	IDs []string `json:"ids,omitempty"`
	All bool     `json:"all,omitempty"`
}

func (s *Service) handleRead(w http.ResponseWriter, r *http.Request) {
	var req readRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !req.All && len(req.IDs) == 0 {
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "ids or all is required", Field: "ids"})
		return
	}
	if len(req.IDs) > InboxRetain {
		httpx.Fail(w, httpx.BadRequest("too many ids"))
		return
	}
	if _, err := s.MarkRead(r.Context(), req.IDs, req.All); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), r.PathValue("id")); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

// handleNotify is for the CLI, hooks and automation: local socket or API
// token. Browser sessions cannot inject notifications.
func (s *Service) handleNotify(w http.ResponseWriter, r *http.Request) {
	if p := server.PrincipalFrom(r.Context()); p == nil || (p.Method != "local" && p.Method != "token") {
		httpx.Fail(w, httpx.Forbidden("notifications can only be sent from the local socket or with an API token"))
		return
	}
	var req api.NotifyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	n, err := s.Notify(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, n)
}

func (s *Service) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, s.apiSettings(r.Context(), st))
}

func (s *Service) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var p settingsPatch
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, err)
		return
	}
	st, err := s.updateSettings(r.Context(), p)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, s.apiSettings(r.Context(), st))
}

func (s *Service) handleKey(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, map[string]string{"publicKey": s.vapidPublic})
}

func (s *Service) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var sub api.PushSubscription
	if err := httpx.Decode(r, &sub); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.Subscribe(r.Context(), sub, r.UserAgent()); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Endpoint == "" {
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "endpoint is required", Field: "endpoint"})
		return
	}
	if err := s.Unsubscribe(r.Context(), req.Endpoint); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

// handleTest sends a push to every subscribed device synchronously so the
// UI can report success or failure. It bypasses rules and quiet hours and
// does not create an inbox entry.
func (s *Service) handleTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*pushTimeout)
	defer cancel()
	id := "n_test" + s.now().UTC().Format("150405")
	n := api.Notification{
		ID:       id,
		Kind:     "system",
		Title:    "Relay test notification",
		Body:     "Push notifications are working on this device.",
		Link:     "/settings/notifications",
		At:       s.now().UTC(),
		Severity: "success",
	}
	sent, err := s.sendPush(ctx, n)
	switch {
	case errors.Is(err, errNoDevices):
		httpx.Fail(w, httpx.Conflict("no devices are subscribed to push notifications"))
	case err != nil:
		s.log().Warn("notify: test push failed", "err", err)
		httpx.Fail(w, &httpx.Err{Status: http.StatusBadGateway, Code: "push_failed", Message: "the push service rejected the notification", RetryIn: int((5 * time.Second).Seconds())})
	case sent == 0:
		httpx.Fail(w, &httpx.Err{Status: http.StatusBadGateway, Code: "push_failed", Message: "no device accepted the notification"})
	default:
		httpx.NoContent(w)
	}
}
