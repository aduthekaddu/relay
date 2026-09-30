package workspaces

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// busAudit is the backend-only audit topic (core.BusAudit once the auth
// feature lands it); internal/auth accepts api.AuditEntry payloads.
const busAudit = "audit"

// audit publishes an audit entry for a destructive git action.
func (s *Service) audit(r *http.Request, event, detail string) {
	e := api.AuditEntry{Event: event, Actor: "system", Detail: detail}
	if len(e.Detail) > 200 {
		e.Detail = e.Detail[:200]
	}
	if p := server.PrincipalFrom(r.Context()); p != nil {
		e.Actor = p.User
		if p.Method == "local" || e.Actor == "" {
			e.Actor = "local"
		}
	}
	e.IP = httpx.ClientIP(r, nil)
	s.publish(busAudit, e)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ws, err := s.List(ctx)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, ws)
}

func (s *Service) handlePin(w http.ResponseWriter, r *http.Request) {
	var req api.PinWorkspaceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ws, err := s.pin(r.Context(), req.Path, req.Pinned)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, ws)
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.status(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, st)
}

func (s *Service) handleDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, err := s.diff(r.Context(), q.Get("path"), q.Get("file"), httpx.QueryBool(r, "staged"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, d)
}

func (s *Service) handleLog(w http.ResponseWriter, r *http.Request) {
	cs, err := s.logOf(r.Context(), r.URL.Query().Get("path"), httpx.QueryInt(r, "limit", 30, 1, 500))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, cs)
}

// action decodes a GitActionRequest and runs fn, answering GitStatus.
func (s *Service) action(w http.ResponseWriter, r *http.Request, event string,
	fn func(context.Context, api.GitActionRequest) (*api.GitStatus, error)) {
	var req api.GitActionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	st, err := fn(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if event != "" {
		detail := st.Root
		switch event {
		case "git.discard":
			detail += " (" + plural(len(req.Files), "file") + ")"
		case "git.commit":
			if st.Last != nil {
				detail += " " + st.Last.Short
			}
		}
		s.audit(r, event, detail)
	}
	httpx.OK(w, st)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func (s *Service) handleStage(w http.ResponseWriter, r *http.Request) {
	s.action(w, r, "", s.stage)
}

func (s *Service) handleUnstage(w http.ResponseWriter, r *http.Request) {
	s.action(w, r, "", s.unstage)
}

func (s *Service) handleDiscard(w http.ResponseWriter, r *http.Request) {
	s.action(w, r, "git.discard", s.discard)
}

func (s *Service) handleCommit(w http.ResponseWriter, r *http.Request) {
	s.action(w, r, "git.commit", s.commit)
}

func (s *Service) handlePush(w http.ResponseWriter, r *http.Request) {
	s.gitTask(w, r, "push")
}

func (s *Service) handlePull(w http.ResponseWriter, r *http.Request) {
	s.gitTask(w, r, "pull")
}

func (s *Service) gitTask(w http.ResponseWriter, r *http.Request, verb string) {
	var req api.GitActionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	t, err := s.task(ctx, req.Path, verb)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if verb == "push" {
		s.audit(r, "git.push", t.Cwd)
	}
	httpx.JSON(w, http.StatusAccepted, api.GitTaskResponse{Terminal: t})
}

func (s *Service) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	var req api.GitActionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	wt, err := s.CreateWorktree(r.Context(), req.Path, req.Branch, req.Base)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "git.worktree.add", wt.Path)
	httpx.OK(w, wt)
}

func (s *Service) handleRemoveWorktree(w http.ResponseWriter, r *http.Request) {
	path, err := s.removeWorktree(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "git.worktree.remove", path)
	httpx.NoContent(w)
}
