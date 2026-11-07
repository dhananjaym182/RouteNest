package adminsso

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var envSecretName = regexp.MustCompile(`^ROUTENEST_[A-Z0-9_]+$`)

type SecretResolver struct {
	mu      sync.RWMutex
	roots   []secretRoot
	hmacKey []byte
	closed  bool
}

type secretRoot struct {
	path string
	dir  *os.Root
}

func NewSecretResolver(allowedRoots []string) (*SecretResolver, error) {
	r := &SecretResolver{}
	for _, root := range allowedRoots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("resolve approved secret root: %w", err), r.Close())
		}
		dir, err := os.OpenRoot(absolute)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("open approved secret root: %w", err), r.Close())
		}
		r.roots = append(r.roots, secretRoot{path: filepath.Clean(absolute), dir: dir})
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, errors.Join(fmt.Errorf("generate secret fingerprint key: %w", err), r.Close())
	}
	r.hmacKey = key
	return r, nil
}

// Close releases pinned directories after in-flight resolutions finish.
func (r *SecretResolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var err error
	for _, root := range r.roots {
		err = errors.Join(err, root.dir.Close())
	}
	clear(r.hmacKey)
	return err
}

func (r *SecretResolver) Resolve(ref SecretRef) ([]byte, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, "", fmt.Errorf("secret resolver is closed")
	}
	if err := ref.validate(); err != nil {
		return nil, "", err
	}
	var value []byte
	switch ref.Kind {
	case "env":
		if !envSecretName.MatchString(ref.Name) {
			return nil, "", fmt.Errorf("environment secret name must use ROUTENEST_* form")
		}
		raw, ok := os.LookupEnv(ref.Name)
		if !ok || raw == "" {
			return nil, "", fmt.Errorf("environment secret is unavailable")
		}
		value = []byte(raw)
	case "file":
		absolute, err := filepath.Abs(ref.Name)
		if err != nil {
			return nil, "", fmt.Errorf("secret file is outside approved roots")
		}
		root, relative := r.rootFor(absolute)
		if root == nil {
			return nil, "", fmt.Errorf("secret file is outside approved roots")
		}
		value, err = readSecretFile(root.dir, relative)
		if err != nil {
			return nil, "", err
		}
		trimmed := append([]byte(nil), bytes.TrimSpace(value)...)
		clear(value)
		value = trimmed
	}
	if len(value) == 0 || len(value) > 1<<20 {
		clear(value)
		return nil, "", fmt.Errorf("invalid secret length")
	}
	mac := hmac.New(sha256.New, r.hmacKey)
	_, _ = mac.Write(value)
	fingerprint := hex.EncodeToString(mac.Sum(nil))[:16]
	return value, fingerprint, nil
}

func (r *SecretResolver) rootFor(path string) (*secretRoot, string) {
	path = filepath.Clean(path)
	for i := range r.roots {
		root := &r.roots[i]
		relative, err := filepath.Rel(root.path, path)
		if err == nil && filepath.IsLocal(relative) {
			return root, relative
		}
	}
	return nil, ""
}

func readSecretFile(root *os.Root, name string) ([]byte, error) {
	// Open within the pinned root, then inspect and read the same descriptor.
	// os.Root rejects symlink escapes even if a path component is replaced.
	file, err := openSecretFile(root, name)
	if err != nil {
		return nil, fmt.Errorf("open secret file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret file is unavailable or not regular")
	}
	if err := validateSecretFile(name, info); err != nil {
		return nil, err
	}
	if info.Size() > 1<<20 {
		return nil, fmt.Errorf("invalid secret length")
	}
	value, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(value) > 1<<20 {
		clear(value)
		return nil, fmt.Errorf("secret file exceeds size limit or is unreadable")
	}
	return value, nil
}
