package cline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
