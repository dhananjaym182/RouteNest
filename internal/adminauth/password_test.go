package adminauth

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func fastHasher(t *testing.T) *PasswordHasher {
	t.Helper()
	h, e := NewPasswordHasher(PasswordParams{19 * 1024, 2, 1, 16, 32}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func TestPasswordPHC(t *testing.T) {
	h := fastHasher(t)
	phc, e := h.Hash(context.Background(), "correct horse battery")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		p    string
		want bool
	}{{"correct horse battery", true}, {"wrong horse battery", false}, {"", false}} {
		ok, e := h.Verify(context.Background(), phc, tc.p)
		if e != nil || ok != tc.want {
			t.Fatalf("verify=%v %v", ok, e)
		}
	}
	if h.NeedsRehash(phc) {
		t.Fatal("unexpected rehash")
	}
	if _, e = h.Hash(context.Background(), "short"); e == nil {
		t.Fatal("short password accepted")
	}
	if _, e = h.Hash(context.Background(), strings.Repeat("x", 1025)); e == nil {
		t.Fatal("oversized password accepted")
	}
}

func TestPasswordMinimumCharactersAndByteLimit(t *testing.T) {
	h := fastHasher(t)
	for _, tc := range []struct {
		name     string
		password string
		accepted bool
	}{
		{"seven ASCII characters", "Seven7!", false},
		{"eight ASCII characters", "Eight8!!", true},
		{"seven Chinese characters", strings.Repeat("密", 7), false},
		{"eight Chinese characters", strings.Repeat("密", 8), true},
		{"seven supplementary characters", strings.Repeat("🔒", 7), false},
		{"eight supplementary characters", strings.Repeat("🔒", 8), true},
		{"ASCII byte limit", strings.Repeat("x", 1024), true},
		{"over ASCII byte limit", strings.Repeat("x", 1025), false},
		{"Unicode byte limit", strings.Repeat("🔒", 256), true},
		{"over Unicode byte limit", strings.Repeat("🔒", 257), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phc, err := h.Hash(context.Background(), tc.password)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v, want %v: %v", err == nil, tc.accepted, err)
			}
			if !tc.accepted {
				return
			}
			if ok, err := h.Verify(context.Background(), phc, tc.password); err != nil || !ok {
				t.Fatalf("accepted password cannot authenticate: %v", err)
			}
		})
	}
}

func TestPHCParserBounds(t *testing.T) {
	salt := "MDEyMzQ1Njc4OWFiY2RlZg"
	key := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"
	good := "$argon2id$v=19$m=19456,t=2,p=1$" + salt + "$" + key
	if _, e := ParsePHC(good); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{strings.Replace(good, "m=19456", "m=19455", 1), strings.Replace(good, "m=19456", "m=262145", 1), strings.Replace(good, "t=2", "t=11", 1), strings.Replace(good, "p=1", "p=256", 1), strings.Replace(good, "p=1", "p=9", 1), strings.Replace(good, "p=1", "p=0", 1), strings.Replace(good, "m=19456", "m=019456", 1), strings.Replace(good, "v=19", "v=16", 1), strings.Replace(good, salt, "QQ", 1), strings.Replace(good, key, "QQ", 1), good + "=", strings.Repeat("x", 513)} {
		if _, e := ParsePHC(s); e == nil {
			t.Fatalf("accepted malformed PHC %q", s)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestPasswordRandomFailureAndDeadline(t *testing.T) {
	h := fastHasher(t)
	h.random = failingReader{}
	if _, e := h.Hash(context.Background(), "correct horse battery"); e == nil {
		t.Fatal("RNG failure accepted")
	}
	h.semaphore = make(chan struct{}, 1)
	h.semaphore <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, e := h.derive(ctx, func() []byte { t.Error("unexpected KDF"); return nil }); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	<-h.semaphore
}
func TestKDFSemaphoreSurvivesDeadline(t *testing.T) {
	h := fastHasher(t)
	h.semaphore = make(chan struct{}, 1)
	h.deadline = time.Millisecond
	release := make(chan struct{})
	started := make(chan struct{})
	_, e := h.derive(context.Background(), func() []byte { close(started); <-release; return nil })
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	<-started
	if len(h.semaphore) != 1 {
		t.Fatal("timed-out KDF released memory permit early")
	}
	close(release)
	deadline := time.After(time.Second)
	for len(h.semaphore) != 0 {
		select {
		case <-deadline:
			t.Fatal("permit leaked")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
func BenchmarkArgon2idDefault(b *testing.B) {
	h, e := NewPasswordHasher(DefaultPasswordParams, nil)
	if e != nil {
		b.Fatal(e)
	}
	phc, e := h.Hash(context.Background(), "correct horse battery")
	if e != nil {
		b.Fatal(e)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ok, e := h.Verify(context.Background(), phc, "correct horse battery"); e != nil || !ok {
				b.Fatal(e)
			}
		}
	})
}
