package info

import (
	"context"
	"sync"
	"testing"
)

// TestSettingsInfoConcurrent exercises authenticated PATCH alongside both
// HTTP readers and the live hello provider. The old suite missed this path.
func TestSettingsInfoConcurrent(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 1000; j++ {
				f.svc.Info(context.Background())
				for _, path := range []string{"/api/v1/info", "/api/v1/settings"} {
					if w := f.do(t, "GET", path, "", true); w.Code != 200 {
						t.Errorf("GET %s: %d", path, w.Code)
						return
					}
				}
			}
		}()
	}
	close(start)
	for i := 0; i < 100; i++ {
		body := `{"recordAgents":true}`
		if i%2 == 0 {
			body = `{"recordAgents":false}`
		}
		if w := f.do(t, "PATCH", "/api/v1/settings", body, true); w.Code != 200 {
			t.Errorf("PATCH: %d %s", w.Code, w.Body.String())
			break
		}
	}
	wg.Wait()
}
