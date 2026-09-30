package ptyd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// stateVersion is bumped on incompatible changes to sessions.json.
const stateVersion = 1

type stateDoc struct {
	Version  int      `json:"version"`
	Sessions []record `json:"sessions"`
}

// record is one persisted session: its last known metadata and the spec
// needed to start it again ("Restore").
type record struct {
	Session api.TerminalSession  `json:"session"`
	Spec    ptyclient.CreateSpec `json:"spec"`
	Renamed bool                 `json:"renamed,omitempty"`
}

// markDirty schedules a save (coalesced by saveLoop).
func (d *Daemon) markDirty() { signal(d.dirty) }

func (d *Daemon) saveLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.dirty:
		}
		if err := d.save(); err != nil {
			d.log.Warn("save session metadata", "err", err)
		}
		// Coalesce bursts (many sessions exiting at once).
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// save writes sessions.json atomically (0600 in a 0700 directory).
func (d *Daemon) save() error {
	d.mu.RLock()
	doc := stateDoc{Version: stateVersion, Sessions: make([]record, 0, len(d.sessions))}
	for _, s := range d.sessions {
		s.mu.Lock()
		info := s.infoLocked()
		info.Clients = 0
		doc.Sessions = append(doc.Sessions, record{Session: *info, Spec: s.spec, Renamed: s.renamed})
		s.mu.Unlock()
	}
	d.mu.RUnlock()
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sessions: %w", err)
	}
	dir := filepath.Dir(d.stateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".sessions-*.json")
	if err != nil {
		return fmt.Errorf("state temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write sessions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), d.stateFile); err != nil {
		return fmt.Errorf("replace sessions: %w", err)
	}
	return nil
}

// load restores metadata written by a previous daemon. Sessions that were
// still running are gone (their pty died with that daemon): they come back
// as exited with meta lost=1 so the UI can offer Restore.
func (d *Daemon) load() error {
	b, err := os.ReadFile(d.stateFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read sessions: %w", err)
	}
	var doc stateDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("decode %s: %w", d.stateFile, err)
	}
	if doc.Version != stateVersion {
		return fmt.Errorf("%s: unsupported version %d", d.stateFile, doc.Version)
	}
	now := time.Now().UTC()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range doc.Sessions {
		if !validID(r.Session.ID) {
			continue
		}
		info := r.Session
		info.Clients = 0
		info.Attention = nil
		if info.Activity != api.ActivityExited {
			info.Activity = api.ActivityExited
			info.ExitedAt = now
			info.Pid = 0
			if info.Meta == nil {
				info.Meta = map[string]string{}
			}
			info.Meta["lost"] = "1"
		}
		s := &Session{
			d:       d,
			info:    info,
			spec:    r.Spec,
			renamed: r.Renamed,
			lost:    info.Meta["lost"] == "1",
			clients: map[*client]struct{}{},
			done:    make(chan struct{}),
		}
		close(s.done)
		d.sessions[info.ID] = s
	}
	return nil
}

// Restore starts a new session from the spec of an exited one.
func (d *Daemon) Restore(id string) (*api.TerminalSession, error) {
	s, err := d.get(id)
	if err != nil {
		return nil, err
	}
	select {
	case <-s.done:
	default:
		return nil, conflict("session is still running")
	}
	s.mu.Lock()
	spec := s.spec
	spec.Meta = copyMeta(spec.Meta)
	if !s.renamed {
		spec.Name = ""
	} else {
		spec.Name = s.info.Name
	}
	s.mu.Unlock()
	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}
	spec.Meta["restoredFrom"] = id
	return d.Create(spec)
}
