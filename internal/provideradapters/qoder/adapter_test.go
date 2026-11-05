package qoder

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"routenest/internal/provideradapters"
)

func TestAdapterRejectsMissingModelWithoutLeakingCredential(t *testing.T) {
	const secret = "dt-super-secret"
	_, err := New().Invoke(context.Background(), provideradapters.Invocation{
		BaseURL: "https://qoder.invalid",
		Credential: provideradapters.Credential{
			Token:    secret,
			Metadata: map[string]string{"userId": "u", "machineId": "m"},
		},
		Params: map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing a model") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked credential: %v", err)
	}
}

func TestAdapterRequiresLoggedInCredential(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	})}
	_, err := New().Invoke(context.Background(), provideradapters.Invocation{
		HTTP: client, BaseURL: "https://api3.qoder.sh",
		Params: map[string]any{"model": "qoder/auto"},
	})
	if err == nil || !strings.Contains(err.Error(), "logged-in connection") {
		t.Fatalf("error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
