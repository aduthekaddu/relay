package ptyclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestDeleteOlderDaemonDoesNotMutate(t *testing.T) {
	root, err := os.MkdirTemp("", "r25-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "ptyd.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var deletes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) { deletes.Add(1); w.WriteHeader(204) })
	srv := &http.Server{Handler: mux}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close(); <-done })
	c := New(socket)
	for _, spec := range []DeleteSpec{{}, {Forget: true}, {Signal: "KILL"}} {
		if _, err := c.Delete(context.Background(), "t_aaaaaaaaaa", spec); !errors.Is(err, ErrNotFound) {
			t.Fatalf("older daemon = %v", err)
		}
	}
	if deletes.Load() != 0 {
		t.Fatal("unsupported outcome request invoked compatibility mutation")
	}
}
