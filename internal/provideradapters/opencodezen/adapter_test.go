package opencodezen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
	upstreamzen "routenest/internal/upstream/zen"
)

func TestAdapterInvokesZenWithRegisteredSession(t *testing.T) {
	const pooledSession = "ses_aaaaaaaaaaaaAAAAAAAAAAAAAA"
	const explicitSession = "ses_bbbbbbbbbbbbBBBBBBBBBBBBBB"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-opencode-session") != explicitSession || r.Header.Get("x-opencode-model") != "model-free" {
			t.Fatalf("identity headers = %+v", r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := New().Invoke(context.Background(), provideradapters.Invocation{
		HTTP: srv.Client(), BaseURL: srv.URL + "/inference/v1", APIKey: "public",
		Protocol: upstreamzen.ProtocolChat, ModelID: "model-free", Body: []byte(`{}`),
		Params:     map[string]any{"metadata": map[string]any{"session_id": "conversation-1"}},
		SessionID:  explicitSession,
		SessionIDs: []string{pooledSession},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestAdapterTreatsZen503AsRateLimited(t *testing.T) {
	before := time.Now()
	got := New().ClassifyFailure(provideradapters.FailureInput{
		Scope:      domain.AvailabilityScope{ProviderID: "opencode", ModelID: "model-free"},
		StatusCode: http.StatusServiceUnavailable,
		Header:     make(http.Header),
	})
	if len(got) != 1 || got[0].Kind != domain.ObservationRateLimited || got[0].Until.Before(before.Add(30*time.Second)) {
		t.Fatalf("observations = %+v", got)
	}
}

func TestAdapterDiscoversModelsWithZenIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer public" || r.Header.Get("x-opencode-client") != "cli" {
			t.Fatalf("identity headers = %+v", r.Header)
		}
		if r.Header.Get("x-opencode-session") == "" || r.Header.Get("X-Custom") != "value" {
			t.Fatalf("catalogue headers = %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"model-b"},{"id":"model-a"}]}`))
	}))
	defer srv.Close()

	entries, err := New().DiscoverModels(context.Background(), provideradapters.DiscoveryInput{
		HTTP: srv.Client(), BaseURL: srv.URL + "/inference/v1", APIKey: "public",
		Headers: map[string]string{"X-Custom": "value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != "model-b" || entries[1].ID != "model-a" {
		t.Fatalf("entries = %+v", entries)
	}
}
