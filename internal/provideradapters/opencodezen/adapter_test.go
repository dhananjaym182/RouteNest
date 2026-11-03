package opencodezen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"routenest/internal/provideradapters"
	upstreamzen "routenest/internal/upstream/zen"
)

func TestAdapterInvokesZenWithRegisteredSession(t *testing.T) {
	const sessionID = "ses_aaaaaaaaaaaaAAAAAAAAAAAAAA"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-opencode-session") != sessionID || r.Header.Get("x-opencode-model") != "model-free" {
			t.Fatalf("identity headers = %+v", r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := New().Invoke(context.Background(), provideradapters.Invocation{
		HTTP: srv.Client(), BaseURL: srv.URL + "/inference/v1", APIKey: "public",
		Protocol: upstreamzen.ProtocolChat, ModelID: "model-free", Body: []byte(`{}`),
		Params:     map[string]any{"metadata": map[string]any{"session_id": "conversation-1"}},
		SessionIDs: []string{sessionID},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
