package agents

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// maxHookBody caps POST /api/v1/agents/hook (payload ≤ 1 MiB + envelope).
const maxHookBody = 1<<20 + 4<<10

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	httpx.OK(w, s.List(ctx))
}

func (s *Service) handleSessions(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := sessionQuery{
		Agent: v.Get("agent"), Q: v.Get("q"), Cwd: v.Get("cwd"), Status: v.Get("status"),
		Archived: v.Get("archived"), Limit: httpx.QueryInt(r, "limit", 50, 1, 200), Cursor: v.Get("cursor"),
	}
	switch q.Status {
	case "", "all", "live", "history":
	default:
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "status must be live, history or all", Field: "status"})
		return
	}
	if p := v.Get("pinned"); p != "" {
		b := httpx.QueryBool(r, "pinned")
		q.Pinned = &b
	}
	if len(q.Q) > maxSearchQuery {
		q.Q = truncate(q.Q, maxSearchQuery)
	}
	page, err := s.listSessions(r.Context(), q)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, page)
}

func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	as, _, err := s.getSession(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, as)
}

func (s *Service) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateAgentSessionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	as, err := s.patchSession(r.Context(), r.PathValue("id"), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, as)
}

func (s *Service) handleTranscript(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	limit := httpx.QueryInt(r, "limit", 200, 1, 1000)
	t, err := s.transcript(ctx, r.PathValue("id"), limit, r.URL.Query().Get("before"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, t)
}

func (s *Service) handleResume(w http.ResponseWriter, r *http.Request) {
	var req api.ResumeAgentRequest
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	t, err := s.resume(ctx, r.PathValue("id"), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, t)
}

func (s *Service) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req api.LaunchAgentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	t, err := s.launch(ctx, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Worktree != nil {
		s.audit(r, "agent.worktree", req.Agent+" in "+t.Cwd)
	}
	httpx.OK(w, t)
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !s.search.allow() {
		httpx.Fail(w, &httpx.Err{Status: 429, Code: "rate_limited", Message: "too many searches", RetryIn: 1})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	hits, err := s.searchText(ctx, r.URL.Query().Get("q"), r.URL.Query().Get("agent"), httpx.QueryInt(r, "limit", 30, 1, 100))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, hits)
}

func (s *Service) handleUsage(w http.ResponseWriter, r *http.Request) {
	sum, err := s.usageSummary(r.Context(), r.URL.Query().Get("range"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, sum)
}

func (s *Service) handleQuotas(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	httpx.OK(w, s.quotas(ctx))
}

func (s *Service) handleReindex(w http.ResponseWriter, r *http.Request) {
	full := httpx.QueryBool(r, "full")
	started := s.idx.trigger(full)
	httpx.JSON(w, http.StatusAccepted, api.ReindexResponse{Started: started})
}

func (s *Service) handleHook(w http.ResponseWriter, r *http.Request) {
	var req api.AgentHookRequest
	if err := httpx.DecodeLimit(r, &req, maxHookBody); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	res, err := s.handleHookEvent(ctx, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, res)
}

func (s *Service) handleInstallHooks(w http.ResponseWriter, r *http.Request) {
	s.changeHooks(w, r, true)
}

func (s *Service) handleRemoveHooks(w http.ResponseWriter, r *http.Request) {
	s.changeHooks(w, r, false)
}

func (s *Service) changeHooks(w http.ResponseWriter, r *http.Request, install bool) {
	a, err := s.adapter(r.PathValue("agent"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if a.Hooks == nil {
		httpx.Fail(w, httpx.Conflict(a.Name+" has no hooks Relay can install"))
		return
	}
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	var st api.HookStatus
	if install {
		if s.relayPath == "" {
			httpx.Fail(w, httpx.Unavailable("cannot locate the relay executable"))
			return
		}
		st, err = a.Hooks.install(s.env(), s.relayPath)
	} else {
		st, err = a.Hooks.remove(s.env())
	}
	if err != nil {
		var he *httpx.Err
		if !errors.As(err, &he) {
			s.log().Warn("agents: hooks", "agent", a.ID, "install", install, "err", err)
			err = httpx.Conflict("could not update " + a.Name + " configuration: " + err.Error())
		}
		httpx.Fail(w, err)
		return
	}
	ev := "agent.hooks.remove"
	if install {
		ev = "agent.hooks.install"
	}
	s.audit(r, ev, a.ID+" "+st.Path)
	httpx.OK(w, st)
}
