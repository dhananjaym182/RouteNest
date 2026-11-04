package cline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
)

func TestAdapterInvokesClineEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer cline-token" || r.Header.Get("X-Task-ID") != "task-1" {
			t.Fatalf("headers = %+v", r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := New().Invoke(context.Background(), provideradapters.Invocation{
		HTTP: srv.Client(), BaseURL: srv.URL + "/api/v1", SessionID: "task-1",
		Credential: provideradapters.Credential{Token: "cline-token"}, Body: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestAdapterClassifiesClineQuotaReset(t *testing.T) {
	before := time.Now()
	got := New().ClassifyFailure(provideradapters.FailureInput{
		Scope:      domain.AvailabilityScope{ProviderID: "cline", CredentialID: "account", ModelID: "model"},
		StatusCode: http.StatusTooManyRequests,
		Body:       []byte(`{"message":"Try again in 1h 1m"}`),
	})
	if len(got) != 1 || got[0].Kind != domain.ObservationQuotaExhausted || got[0].ResetAt.Before(before.Add(61*time.Minute)) {
		t.Fatalf("observations = %+v", got)
	}
}

func TestAdapterDiscoversRecommendedModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recommended" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"free":[{"id":"free/model","name":"Free","tags":["fast"]}],
			"clinePass":[{"id":"pass/model"}],
			"recommended":[{"id":"recommended/model"}]
		}`))
	}))
	defer srv.Close()

	entries, err := New().DiscoverModels(context.Background(), provideradapters.DiscoveryInput{
		HTTP: srv.Client(), BaseURL: srv.URL + "/recommended",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].ID != "free/model" || !entries[0].FreeFlag || entries[1].ID != "pass/model" || entries[2].ID != "recommended/model" {
		t.Fatalf("entries = %+v", entries)
	}
}
