package schedule

import (
	"net/http"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// Routes registers /api/v1/schedules.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/schedules", s.handleList)
	rt.Handle("POST /api/v1/schedules", s.handleCreate)
	rt.Handle("GET /api/v1/schedules/describe", s.handleDescribe)
	rt.Handle("GET /api/v1/schedules/{id}", s.handleGet)
	rt.Handle("PATCH /api/v1/schedules/{id}", s.handleUpdate)
	rt.Handle("DELETE /api/v1/schedules/{id}", s.handleDelete)
	rt.Handle("POST /api/v1/schedules/{id}/run", s.handleRun)
	rt.Handle("GET /api/v1/schedules/{id}/runs", s.handleRuns)
}

func respond[T any](w http.ResponseWriter, v T, err error) {
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, v)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	v, err := s.List(r.Context())
	respond(w, v, err)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.Get(r.Context(), r.PathValue("id"))
	respond(w, v, err)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var p patch
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := s.Create(r.Context(), p)
	respond(w, v, err)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var p patch
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := s.Update(r.Context(), r.PathValue("id"), p)
	respond(w, v, err)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), r.PathValue("id")); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) handleRun(w http.ResponseWriter, r *http.Request) {
	v, err := s.Run(r.Context(), r.PathValue("id"))
	respond(w, v, err)
}

func (s *Service) handleRuns(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Get(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := s.Runs(r.Context(), id, httpx.QueryInt(r, "limit", 20, 1, runsKept))
	respond(w, v, err)
}

// handleDescribe previews a cron expression without saving anything.
func (s *Service) handleDescribe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	expr, tz := q.Get("cron"), q.Get("timezone")
	next, err := NextRuns(expr, tz, s.now(), httpx.QueryInt(r, "count", 3, 1, 10))
	if err != nil {
		httpx.OK(w, api.CronPreview{Valid: false, Error: err.Error()})
		return
	}
	httpx.OK(w, api.CronPreview{Valid: true, Description: Describe(expr), Next: next})
}
