package agents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// agentDBs opens other tools' SQLite databases strictly read-only.
//
// A database whose write-ahead log is empty is opened in place with
// mode=ro&immutable=1 (SQLite then never touches the file, its -wal or
// -shm). When the WAL holds data, immutable mode would miss the newest
// rows, so the database and WAL are copied into Relay's private scratch
// directory and the copy is opened instead. Handles are cached until the
// source files change.
type agentDBs struct {
	mu   sync.Mutex
	open map[string]*agentDB // source path -> handle
}

type agentDB struct {
	db    *sql.DB
	stamp string // size/mtime of db + wal when opened
	copy  string // snapshot path to delete on close ("" when in place)
}

func newAgentDBs() *agentDBs { return &agentDBs{open: map[string]*agentDB{}} }

func fileStamp(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano()), true
}

// get returns a read-only handle for path, refreshing it when the file
// changed. The handle stays owned by the cache.
func (c *agentDBs) get(ctx context.Context, tmp, path string) (*sql.DB, error) {
	dbStamp, ok := fileStamp(path)
	if !ok {
		return nil, fmt.Errorf("%s: not found", filepath.Base(path))
	}
	walStamp, _ := fileStamp(path + "-wal")
	stamp := dbStamp + "|" + walStamp
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.open[path]; ok {
		if h.stamp == stamp {
			return h.db, nil
		}
		h.close()
		delete(c.open, path)
	}
	h, err := openAgentDB(ctx, tmp, path)
	if err != nil {
		return nil, err
	}
	h.stamp = stamp
	c.open[path] = h
	return h.db, nil
}

func (h *agentDB) close() {
	_ = h.db.Close()
	if h.copy != "" {
		for _, sfx := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(h.copy + sfx)
		}
	}
}

// closeAll releases every handle and snapshot.
func (c *agentDBs) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, h := range c.open {
		h.close()
		delete(c.open, k)
	}
}

func openAgentDB(ctx context.Context, tmp, path string) (*agentDB, error) {
	src := path
	h := &agentDB{}
	if st, err := os.Stat(path + "-wal"); err == nil && st.Size() > 0 {
		if tmp == "" {
			tmp = os.TempDir()
		}
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(path))
		dst := filepath.Join(tmp, "agentdb-"+hex.EncodeToString(sum[:6])+".db")
		if err := copyFile(ctx, path, dst); err != nil {
			return nil, err
		}
		if err := copyFile(ctx, path+"-wal", dst+"-wal"); err != nil {
			_ = os.Remove(dst)
			return nil, err
		}
		src, h.copy = dst, dst
	}
	q := url.Values{}
	if h.copy == "" {
		// In place: never write, never create -shm.
		q.Set("mode", "ro")
		q.Set("immutable", "1")
	}
	q.Add("_pragma", "busy_timeout(2000)")
	q.Add("_pragma", "query_only(1)")
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: src}).EscapedPath()+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(time.Minute)
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		db.Close()
		if h.copy != "" {
			_ = os.Remove(h.copy)
			_ = os.Remove(h.copy + "-wal")
		}
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	h.db = db
	return h, nil
}

// copyFile copies src to dst (0600), honouring ctx between blocks.
func copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	buf := make([]byte, 256<<10)
	for {
		if ctx.Err() != nil {
			out.Close()
			return ctx.Err()
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	return out.Close()
}

// tableColumns returns the column names of table (empty when missing), so
// readers adapt to schema drift between agent versions.
func tableColumns(ctx context.Context, db *sql.DB, table string) map[string]bool {
	cols := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return cols
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			cols[n] = true
		}
	}
	return cols
}
