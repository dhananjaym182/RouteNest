package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/argon2"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxUsernameBytes = 128
const MinPasswordCharacters = 8
const MinBootstrapSecretBytes = 12
const MaxPasswordBytes = 1024
const MaxLoginBodyBytes = 4096

type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var DefaultPasswordParams = PasswordParams{64 * 1024, 3, 1, 16, 32}

type ParsedPHC struct {
	Params PasswordParams
	Salt   []byte
	Key    []byte
}

func validateParams(p PasswordParams) error {
	if p.Memory < 19*1024 || p.Memory > 256*1024 || p.Iterations < 2 || p.Iterations > 10 || p.Parallelism < 1 || p.Parallelism > 8 || p.SaltLength < 16 || p.SaltLength > 64 || p.KeyLength < 16 || p.KeyLength > 64 {
		return fmt.Errorf("invalid Argon2id parameters")
	}
	return nil
}
func ParsePHC(s string) (ParsedPHC, error) {
	var p ParsedPHC
	if len(s) > 512 {
		return p, fmt.Errorf("invalid Argon2id encoding")
	}
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return p, fmt.Errorf("invalid Argon2id encoding")
	}
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return p, fmt.Errorf("invalid Argon2id parameters")
	}
	values := make([]uint64, 3)
	names := []string{"m=", "t=", "p="}
	for i, f := range fields {
		if !strings.HasPrefix(f, names[i]) {
			return p, fmt.Errorf("invalid Argon2id parameters")
		}
		v, e := strconv.ParseUint(strings.TrimPrefix(f, names[i]), 10, 32)
		if e != nil || strconv.FormatUint(v, 10) != strings.TrimPrefix(f, names[i]) {
			return p, fmt.Errorf("invalid Argon2id parameters")
		}
		values[i] = v
	}
	if values[2] > 8 {
		return p, fmt.Errorf("invalid Argon2id parallelism")
	}
	if len(parts[4]) > 86 || len(parts[5]) > 86 {
		return p, fmt.Errorf("invalid Argon2id lengths")
	}
	var err error
	p.Salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return p, fmt.Errorf("invalid Argon2id salt")
	}
	p.Key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return p, fmt.Errorf("invalid Argon2id key")
	}
	p.Params = PasswordParams{uint32(values[0]), uint32(values[1]), uint8(values[2]), uint32(len(p.Salt)), uint32(len(p.Key))}
	return p, validateParams(p.Params)
}

type PasswordHasher struct {
	params    PasswordParams
	semaphore chan struct{}
	random    io.Reader
	deadline  time.Duration
	dummy     string
}

func NewPasswordHasher(params PasswordParams, random io.Reader) (*PasswordHasher, error) {
	if err := validateParams(params); err != nil {
		return nil, err
	}
	if random == nil {
		random = rand.Reader
	}
	n := runtime.GOMAXPROCS(0)
	if n > 2 {
		n = 2
	}
	h := &PasswordHasher{params: params, semaphore: make(chan struct{}, n), random: random, deadline: 5 * time.Second}
	salt := []byte("RouteNestDummyPHC")
	key := argon2.IDKey([]byte("unmatchable-dummy-password"), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	h.dummy = encodePHC(params, salt, key)
	return h, nil
}
func encodePHC(p PasswordParams, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", p.Memory, p.Iterations, p.Parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}
func (h *PasswordHasher) derive(ctx context.Context, fn func() []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, h.deadline)
	defer cancel()
	select {
	case h.semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	done := make(chan []byte, 1)
	go func() { defer func() { <-h.semaphore }(); done <- fn() }()
	select {
	case key := <-done:
		return key, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (h *PasswordHasher) Hash(ctx context.Context, password string) (string, error) {
	if len(password) > MaxPasswordBytes || utf8.RuneCountInString(password) < MinPasswordCharacters {
		return "", fmt.Errorf("password must contain at least %d characters and at most %d bytes", MinPasswordCharacters, MaxPasswordBytes)
	}
	return h.hash(ctx, password)
}

// hash also serves parameter upgrades for already verified passwords, which
// must remain usable even when they predate the current new-password policy.
func (h *PasswordHasher) hash(ctx context.Context, password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", fmt.Errorf("password must contain at most %d bytes", MaxPasswordBytes)
	}
	salt := make([]byte, h.params.SaltLength)
	if _, e := io.ReadFull(h.random, salt); e != nil {
		return "", fmt.Errorf("secure random generation failed: %w", e)
	}
	key, e := h.derive(ctx, func() []byte {
		return argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.KeyLength)
	})
	if e != nil {
		return "", e
	}
	return encodePHC(h.params, salt, key), nil
}
func (h *PasswordHasher) Verify(ctx context.Context, phc, password string) (bool, error) {
	if len(password) > MaxPasswordBytes {
		return false, ErrUnauthorized
	}
	p, e := ParsePHC(phc)
	if e != nil {
		return false, e
	}
	key, e := h.derive(ctx, func() []byte {
		return argon2.IDKey([]byte(password), p.Salt, p.Params.Iterations, p.Params.Memory, p.Params.Parallelism, p.Params.KeyLength)
	})
	if e != nil {
		return false, e
	}
	return subtle.ConstantTimeCompare(key, p.Key) == 1, nil
}
func (h *PasswordHasher) DummyVerify(ctx context.Context, password string) error {
	_, e := h.Verify(ctx, h.dummy, password)
	return e
}
func (h *PasswordHasher) NeedsRehash(phc string) bool {
	p, e := ParsePHC(phc)
	return e != nil || p.Params != h.params
}
func ValidLegacy(c LegacyCredential) bool {
	v, e := hex.DecodeString(c.Hash)
	return e == nil && len(v) == sha256.Size && len(c.Salt) > 0 && len(c.Salt) <= 1024 && c.Hash != LegacyMigrationSentinel
}
func VerifyLegacy(c LegacyCredential, password string) bool {
	if !ValidLegacy(c) || len(password) > MaxPasswordBytes {
		return false
	}
	expected, _ := hex.DecodeString(c.Hash)
	actual := sha256.Sum256([]byte(c.Salt + password))
	return subtle.ConstantTimeCompare(expected, actual[:]) == 1
}
