package accounts

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// Characterizes the CLI login entry (`routenest --login` / `--add-account`) when
// the device-authorization step fails: the call must return the error and must not
// add an account. The success path waits for the device poll interval (at least
// five seconds) and is not covered here.
func TestAddAccountFromDeviceAuthFailureLeavesPoolUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "device authorization unavailable", http.StatusInternalServerError)
	}))
	defer srv.Close()

	oldCfg := clineAuthConfig
	clineAuthConfig.DeviceAuthURL = srv.URL
	oldPool, oldPath := Pool, PoolPath
	t.Cleanup(func() {
		clineAuthConfig = oldCfg
		Pool, PoolPath = oldPool, oldPath
	})
	PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	Pool = &AccountPool{Accounts: []*Account{}, Keys: []string{}}

	acc, err := AddAccountFromDeviceAuth()
	if err == nil {
		t.Fatal("AddAccountFromDeviceAuth returned nil error for a failing device endpoint")
	}
	if acc != nil {
		t.Fatalf("account returned on failure: %+v", acc)
	}
	if n := len(Pool.Accounts); n != 0 {
		t.Fatalf("pool has %d accounts after a failed login, want 0", n)
	}
}
