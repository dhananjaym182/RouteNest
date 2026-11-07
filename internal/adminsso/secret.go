package adminsso

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var envSecretName = regexp.MustCompile(`^ROUTENEST_[A-Z0-9_]+$`)

type SecretResolver struct {
	allowedRoots []string
	hmacKey      []byte
}

func NewSecretResolver(allowedRoots []string) (*SecretResolver, error) {
	roots := make([]string, 0, len(allowedRoots))
	for _, root := range allowedRoots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		roots = append(roots, filepath.Clean(absolute))
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secret fingerprint key: %w", err)
	}
	return &SecretResolver{allowedRoots: roots, hmacKey: key}, nil
}

func (r *SecretResolver) Resolve(ref SecretRef) ([]byte, string, error) {
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
		resolved, err := filepath.EvalSymlinks(ref.Name)
		if err != nil {
			return nil, "", fmt.Errorf("resolve secret file: %w", err)
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil || !r.allowed(absolute) {
			return nil, "", fmt.Errorf("secret file is outside approved roots")
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() {
			return nil, "", fmt.Errorf("secret file is unavailable or not regular")
		}
		if err := validateSecretFile(absolute, info); err != nil {
			return nil, "", err
		}
		value, err = os.ReadFile(absolute)
		if err != nil {
			return nil, "", fmt.Errorf("read secret file: %w", err)
		}
		value = []byte(strings.TrimSpace(string(value)))
	}
	if len(value) == 0 || len(value) > 1<<20 {
		return nil, "", fmt.Errorf("invalid secret length")
	}
	mac := hmac.New(sha256.New, r.hmacKey)
	_, _ = mac.Write(value)
	fingerprint := hex.EncodeToString(mac.Sum(nil))[:16]
	return value, fingerprint, nil
}

func (r *SecretResolver) allowed(path string) bool {
	path = filepath.Clean(path)
	for _, root := range r.allowedRoots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
