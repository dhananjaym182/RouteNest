package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"routenest/internal/provideradapters"
)

func TestAdapterInvokesOpenAICompatibleEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Test"); got != "connection" {
			t.Fatalf("merged header = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"model":"m"}` {
			t.Fatalf("body = %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := New().Invoke(context.Background(), provideradapters.Invocation{
		HTTP: srv.Client(), BaseURL: srv.URL + "/v1", APIKey: "api-key",
		Headers:    map[string]string{"X-Test": "provider"},
		Credential: provideradapters.Credential{Token: "oauth-token", Headers: map[string]string{"X-Test": "connection"}},
		Body:       []byte(`{"model":"m"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestAdapterPreparesBodyFromParams(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := New().Invoke(context.Background(), provideradapters.Invocation{
		HTTP: srv.Client(), BaseURL: srv.URL,
		Params: map[string]any{
			"model": "m", "messages": []any{}, "stream": false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got["model"] != "m" {
		t.Fatalf("prepared body = %#v", got)
	}
}
