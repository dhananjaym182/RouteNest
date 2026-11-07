package adminsso

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestSecretResolverEnvironmentAndClose(t *testing.T) {
	t.Setenv("ROUTENEST_TEST_SECRET", "private-secret")
	resolver, err := NewSecretResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	ref := SecretRef{Kind: "env", Name: "ROUTENEST_TEST_SECRET"}
	value, fingerprint, err := resolver.Resolve(ref)
	if err != nil || string(value) != "private-secret" || len(fingerprint) != 16 {
		t.Fatalf("environment secret = %q, %q, %v", value, fingerprint, err)
	}
	clear(value)
	_, repeated, err := resolver.Resolve(ref)
	if err != nil || repeated != fingerprint {
		t.Fatalf("fingerprint changed without rotation: %q, %v", repeated, err)
	}
	for _, name := range []string{"PATH", "ROUTENEST_TEST_MISSING_SECRET"} {
		if _, _, err := resolver.Resolve(SecretRef{Kind: "env", Name: name}); err == nil {
			t.Fatalf("unavailable or unapproved environment secret %q accepted", name)
		}
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 32 {
				_, _, _ = resolver.Resolve(ref)
			}
		})
	}
	if err := resolver.Close(); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	if _, _, err := resolver.Resolve(ref); err == nil {
		t.Fatal("closed resolver accepted a secret")
	}
	if err := resolver.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSecretResolverFilePlatformBoundary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "secret")
	if err := os.WriteFile(path, []byte(" file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSecretResolver([]string{directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	value, _, err := resolver.Resolve(SecretRef{Kind: "file", Name: path})
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Fatal("Windows file reference accepted without ACL verification")
		}
		return
	}
	if err != nil || string(value) != "file-secret" {
		t.Fatalf("file secret = %q, %v", value, err)
	}
}

func TestSecretResolverRejectsMissingApprovedRoot(t *testing.T) {
	resolver, err := NewSecretResolver([]string{t.TempDir(), filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		_ = resolver.Close()
		t.Fatal("missing approved directory accepted")
	}
}
