package admin_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"routenest/internal/admin"
)

// Admin config updates publish a new configuration; request-path readers keep
// reading the one they hold. Run under -race, this fails if an update edits the
// live configuration in place while readers use it.
func TestConfigUpdatesPublishWholeConfigurations(t *testing.T) {
	installConfig(t, admin.DefaultProxyConfig())
	installPool(t, nil) // no admin password, so the update is not rejected with 401
	harness := newAdminRouteTestHarness(t)
	mux := harness.mux

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cfg := admin.GetProxyConfig()
				if s := cfg.Strategy; s != "round_robin" && s != "random" && s != "fill" {
					t.Errorf("reader saw strategy %q", s)
					return
				}
				_ = cfg.Headers
				_ = cfg.ModelChain
			}
		}()
	}

	strategies := []string{"round_robin", "random"}
	for i := 0; i < 60; i++ {
		body := `{"strategy":"` + strategies[i%2] + `"}`
		req := harness.request(http.MethodPost, "/admin/api/config/update", body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("update %d: status %d body %s", i, rec.Code, rec.Body.String())
		}
	}
	close(stop)
	wg.Wait()

	if got := admin.GetProxyConfig().Strategy; got != "random" {
		t.Fatalf("final strategy = %q, want random (last update)", got)
	}
}
