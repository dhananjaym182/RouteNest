package integration

import (
	"net/http"
	"os"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/adminauth"
	storeadminauth "routenest/internal/store/adminauth"
)

func integrationAdminRegistrar(t *testing.T, canonicalURL string) func(*http.ServeMux) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	repo, err := storeadminauth.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := adminauth.NewService(repo, adminauth.Options{
		PasswordParams:    adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		BootstrapPassword: "one time bootstrap secret",
	})
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return func(mux *http.ServeMux) {
		routes, err := admin.RegisterRoutesWithService(mux, service, admin.RouteSecurityOptions{CanonicalURL: canonicalURL})
		if err != nil {
			panic(err)
		}
		t.Cleanup(func() { _ = routes.Close() })
	}
}
