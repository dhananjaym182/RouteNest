//go:build !windows

package adminsso

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func secretResolverForTest(t *testing.T, root string) *SecretResolver {
	t.Helper()
	resolver, err := NewSecretResolver([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	return resolver
}

func writeSecretForTest(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSecretResolverRejectsFileAndDirectorySymlinkEscapes(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeSecretForTest(t, filepath.Join(root, "secret"), "inside")
	writeSecretForTest(t, filepath.Join(outside, "secret"), "outside")
	resolver := secretResolverForTest(t, root)
	links := map[string]string{
		"relative-escape":  filepath.Join("..", filepath.Base(outside), "secret"),
		"absolute-escape":  filepath.Join(outside, "secret"),
		"directory-escape": outside,
		"inside-link":      "secret",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(outside, "secret"), filepath.Join(root, "relative-escape"), filepath.Join(root, "absolute-escape"), filepath.Join(root, "directory-escape", "secret")} {
		if value, _, err := resolver.Resolve(SecretRef{Kind: "file", Name: path}); err == nil {
			t.Fatalf("symlink escape returned %q from %q", value, path)
		}
	}
	value, _, err := resolver.Resolve(SecretRef{Kind: "file", Name: filepath.Join(root, "inside-link")})
	if err != nil || string(value) != "inside" {
		t.Fatalf("safe relative symlink = %q, %v", value, err)
	}
}

func TestSecretResolverPinsApprovedDirectoryAcrossReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "approved")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSecretForTest(t, filepath.Join(root, "secret"), "inside")
	outside := t.TempDir()
	writeSecretForTest(t, filepath.Join(outside, "secret"), "outside")
	resolver := secretResolverForTest(t, root)
	if err := os.Rename(root, filepath.Join(base, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	value, _, err := resolver.Resolve(SecretRef{Kind: "file", Name: filepath.Join(root, "secret")})
	if err != nil || string(value) != "inside" {
		t.Fatalf("approved directory replacement = %q, %v", value, err)
	}
}

func TestSecretResolverConcurrentSymlinkReplacementCannotEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeSecretForTest(t, filepath.Join(root, "secret"), "inside")
	writeSecretForTest(t, filepath.Join(outside, "secret"), "outside")
	resolver := secretResolverForTest(t, root)
	link := filepath.Join(root, "current")
	if err := os.Symlink("secret", link); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var group sync.WaitGroup
	group.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, target := range []string{"secret", filepath.Join(outside, "secret")} {
				replacement := filepath.Join(root, "replacement")
				if err := os.Symlink(target, replacement); err != nil {
					t.Error(err)
					return
				}
				if err := os.Rename(replacement, link); err != nil {
					t.Error(err)
					return
				}
			}
		}
	})
	defer func() { close(stop); group.Wait() }()
	for range 1000 {
		value, _, err := resolver.Resolve(SecretRef{Kind: "file", Name: link})
		if err == nil && string(value) != "inside" {
			t.Fatalf("concurrent symlink replacement escaped root: %q", value)
		}
	}
}

func TestSecretResolverRejectsUnsafeFilesAndBoundsInput(t *testing.T) {
	root := t.TempDir()
	resolver := secretResolverForTest(t, root)
	unsafe := filepath.Join(root, "unsafe")
	writeSecretForTest(t, unsafe, "secret")
	if err := os.Chmod(unsafe, 0o644); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(root, "oversized")
	// Whitespace is bounded before trimming, too.
	writeSecretForTest(t, oversized, strings.Repeat(" ", (1<<20)+1))
	for _, path := range []string{root, unsafe, oversized} {
		if _, _, err := resolver.Resolve(SecretRef{Kind: "file", Name: path}); err == nil {
			t.Fatalf("unsafe secret accepted: %s", path)
		}
	}
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := resolver.Resolve(SecretRef{Kind: "file", Name: fifo})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as a secret")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked secret resolution")
	}
}
