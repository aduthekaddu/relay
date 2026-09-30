package agents

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Live pairing joins running terminals with indexed agent sessions.
//
// Evidence, strongest first:
//  1. The terminal carries AgentSessionID: set when Relay launched the agent
//     with a pre-chosen id (claude --session-id, pi --session-id) or resumed
//     a known session.
//  2. A hook call from inside the terminal (RELAY_SESSION) named the agent's
//     native session id.
//  3. Heuristic: same agent, same cwd, and a transcript that started between
//     10 s before and 2 min after the terminal was created. A terminal (or a
//     session) with two plausible partners less than pairAmbiguity apart is
//     left unpaired: Relay never borrows a session it is not sure about.
const (
	pairBefore    = 10 * time.Second
	pairAfter     = 2 * time.Minute
	pairAmbiguity = 5 * time.Second
	liveTTL       = 3 * time.Second
)

// pairCand is an indexed session that may belong to a terminal.
type pairCand struct {
	ID      string
	Agent   string
	Cwd     string
	Started time.Time
}

// pairTerminals returns terminal id -> session id.
func pairTerminals(terms []api.TerminalSession, hints map[string]string, cands []pairCand) map[string]string {
	out := map[string]string{}
	claimed := map[string]bool{}
	var rest []api.TerminalSession
	for _, t := range terms {
		if t.Agent == "" || t.Activity == api.ActivityExited {
			continue
		}
		native := t.AgentSessionID
		if native == "" {
			native = hints[t.ID]
		}
		if native != "" {
			id := t.Agent + ":" + native
			out[t.ID] = id
			claimed[id] = true
			continue
		}
		rest = append(rest, t)
	}
	type edge struct {
		term, cand string
		delta      time.Duration
	}
	var edges []edge
	for _, t := range rest {
		cwd := filepath.Clean(t.Cwd)
		for _, c := range cands {
			if c.Agent != t.Agent || claimed[c.ID] || filepath.Clean(c.Cwd) != cwd || c.Started.IsZero() {
				continue
			}
			d := c.Started.Sub(t.CreatedAt)
			if d < -pairBefore || d > pairAfter {
				continue
			}
			if d < 0 {
				d = -d
			}
			edges = append(edges, edge{t.ID, c.ID, d})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].delta != edges[j].delta {
			return edges[i].delta < edges[j].delta
		}
		return edges[i].term+edges[i].cand < edges[j].term+edges[j].cand
	})
	usedTerm, usedCand, ambiguous := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range edges {
		if usedTerm[e.term] || usedCand[e.cand] || ambiguous[e.term] || ambiguous[e.cand] {
			continue
		}
		clash := false
		for _, o := range edges {
			if o == e || usedTerm[o.term] || usedCand[o.cand] {
				continue
			}
			if (o.term == e.term || o.cand == e.cand) && o.delta-e.delta < pairAmbiguity {
				clash = true
				ambiguous[o.term], ambiguous[o.cand] = true, true
			}
		}
		if clash {
			ambiguous[e.term], ambiguous[e.cand] = true, true
			continue
		}
		usedTerm[e.term], usedCand[e.cand] = true, true
		out[e.term] = e.cand
	}
	return out
}

// liveSnapshot is the pairing state at one moment.
type liveSnapshot struct {
	bySession map[string]api.TerminalSession // session id -> terminal
	byTerm    map[string]string              // terminal id -> session id
	unpaired  []api.TerminalSession          // agent terminals without a session
	terms     []api.TerminalSession
}

// liveState caches terminals from ptyd and their pairing.
type liveState struct {
	s     *Service
	mu    sync.Mutex
	snap  liveSnapshot
	at    time.Time
	hints map[string]string // terminal id -> native id (from hooks)
}

func newLiveState(s *Service) *liveState {
	return &liveState{s: s, hints: map[string]string{}}
}

// hint records that terminal id runs native session nativeID.
func (l *liveState) hint(termID, nativeID string) {
	if termID == "" || nativeID == "" || !validNativeID(nativeID) {
		return
	}
	l.mu.Lock()
	changed := l.hints[termID] != nativeID
	l.hints[termID] = nativeID
	if changed {
		l.at = time.Time{}
	}
	l.mu.Unlock()
}

// repair forces the next snapshot to recompute pairings.
func (l *liveState) repair() {
	l.mu.Lock()
	l.at = time.Time{}
	l.mu.Unlock()
}

// snapshot returns the current pairing, refreshing it when stale.
func (l *liveState) snapshot(ctx context.Context) liveSnapshot {
	l.mu.Lock()
	if time.Since(l.at) < liveTTL && l.snap.bySession != nil {
		snap := l.snap
		l.mu.Unlock()
		return snap
	}
	hints := make(map[string]string, len(l.hints))
	for k, v := range l.hints {
		hints[k] = v
	}
	l.mu.Unlock()

	snap := liveSnapshot{bySession: map[string]api.TerminalSession{}, byTerm: map[string]string{}}
	if l.s.d.Pty != nil {
		lctx, cancel := context.WithTimeout(ctx, time.Second)
		terms, err := l.s.d.Pty.List(lctx)
		cancel()
		if err == nil {
			snap.terms = terms
		}
	}
	live := map[string]bool{}
	for _, t := range snap.terms {
		live[t.ID] = true
	}
	pairs := pairTerminals(snap.terms, hints, l.candidates(ctx, snap.terms))
	for _, t := range snap.terms {
		if t.Agent == "" || t.Activity == api.ActivityExited {
			continue
		}
		if sid, ok := pairs[t.ID]; ok {
			snap.bySession[sid] = t
			snap.byTerm[t.ID] = sid
		} else {
			snap.unpaired = append(snap.unpaired, t)
		}
	}
	l.mu.Lock()
	for id := range l.hints {
		if !live[id] && snap.terms != nil {
			delete(l.hints, id) // terminal is gone
		}
	}
	l.snap, l.at = snap, time.Now()
	l.mu.Unlock()
	return snap
}

// candidates loads sessions that started around the unpaired terminals.
func (l *liveState) candidates(ctx context.Context, terms []api.TerminalSession) []pairCand {
	var oldest time.Time
	agents := map[string]bool{}
	for _, t := range terms {
		if t.Agent == "" || t.AgentSessionID != "" || t.Activity == api.ActivityExited {
			continue
		}
		agents[t.Agent] = true
		if oldest.IsZero() || t.CreatedAt.Before(oldest) {
			oldest = t.CreatedAt
		}
	}
	if len(agents) == 0 {
		return nil
	}
	rows, err := l.s.ix.db.QueryContext(ctx, `SELECT id, agent, cwd, started FROM agent_sessions WHERE hidden=0 AND started >= ?`,
		toMS(oldest.Add(-pairBefore)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []pairCand
	for rows.Next() {
		var c pairCand
		var started int64
		if rows.Scan(&c.ID, &c.Agent, &c.Cwd, &started) == nil && agents[c.Agent] {
			c.Started = msTime(started)
			out = append(out, c)
		}
	}
	return out
}

// terminalFor finds the live terminal an agent hook came from.
func (l *liveState) terminalFor(ctx context.Context, agent, relaySession, nativeID, cwd string) (api.TerminalSession, bool) {
	snap := l.snapshot(ctx)
	if relaySession != "" {
		for _, t := range snap.terms {
			if t.ID == relaySession {
				return t, true
			}
		}
		return api.TerminalSession{}, false
	}
	if nativeID != "" {
		if t, ok := snap.bySession[agent+":"+nativeID]; ok {
			return t, true
		}
	}
	// Last resort: exactly one live terminal of this agent in cwd.
	var match []api.TerminalSession
	for _, t := range snap.terms {
		if t.Agent == agent && t.Activity != api.ActivityExited && cwd != "" && filepath.Clean(t.Cwd) == filepath.Clean(cwd) {
			match = append(match, t)
		}
	}
	if len(match) == 1 {
		return match[0], true
	}
	return api.TerminalSession{}, false
}

// loop invalidates the cache on terminal events and announces sessions
// whose live state changed.
func (l *liveState) loop(ctx context.Context) {
	if l.s.d.Bus == nil {
		<-ctx.Done()
		return
	}
	sub := l.s.d.Bus.Subscribe(128, func(ev api.Event) bool {
		return strings.HasPrefix(ev.Type, "terminal.")
	})
	defer sub.Close()
	prev := map[string]string{}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-sub.C:
			if !ok {
				return
			}
			l.repair()
			// Coalesce bursts of terminal events.
			if !sleepCtx(ctx, 300*time.Millisecond) {
				return
			}
			drain(sub.C)
		case <-tick.C:
		}
		snap := l.snapshot(ctx)
		changed := map[string]bool{}
		for term, sid := range snap.byTerm {
			if prev[term] != sid {
				changed[sid] = true
			}
		}
		for term, sid := range prev {
			if snap.byTerm[term] != sid {
				changed[sid] = true
			}
		}
		prev = snap.byTerm
		for sid := range changed {
			l.s.announce(ctx, sid)
		}
	}
}

func drain(c <-chan api.Event) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}
