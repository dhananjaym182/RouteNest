package qoder

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

var testCreds = Creds{
	UserID:    "user-123",
	AuthToken: "dt-test-token",
	Email:     "dev@example.com",
	Name:      "Dev",
	MachineID: "11111111-2222-4333-8444-555555555555",
	BaseURL:   "https://api3.qoder.sh",
}

func TestBuildCosyHeaders(t *testing.T) {
	body := []byte(`{"encoded":"payload"}`)
	rawURL := ChatURL(testCreds.BaseURL)

	h, err := buildCosyHeaders(body, rawURL, testCreds)
	if err != nil {
		t.Fatalf("buildCosyHeaders: %v", err)
	}

	required := []string{
		"Authorization", "Cosy-Key", "Cosy-User", "Cosy-Date", "Cosy-Version",
		"Cosy-Machineid", "Cosy-Machinetoken", "Cosy-Machinetype", "Cosy-Machineos",
		"Cosy-Clienttype", "Cosy-Clientip", "Cosy-Bodyhash", "Cosy-Bodylength",
		"Cosy-Sigpath", "Cosy-Data-Policy", "Cosy-Organization-Id",
		"Cosy-Organization-Tags", "Login-Version", "X-Request-Id",
	}
	if len(h) != len(required) {
		t.Errorf("header count = %d, want %d (%v)", len(h), len(required), h)
	}
	for _, key := range required {
		if _, ok := h[key]; !ok {
			t.Errorf("missing header %q", key)
		}
	}

	// Empty-but-present organization headers are part of the signature.
	if _, ok := h["Cosy-Organization-Id"]; !ok {
		t.Error("Cosy-Organization-Id must be present even when empty")
	}

	auth := h.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer COSY.") {
		t.Fatalf("Authorization = %q, want Bearer COSY. prefix", auth)
	}
	parts := strings.Split(auth, ".")
	if len(parts) != 3 {
		t.Fatalf("Authorization has %d dot-separated parts, want 3: %q", len(parts), auth)
	}
	payloadJSON, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	var payload struct {
		Version     string `json:"version"`
		RequestID   string `json:"requestId"`
		Info        string `json:"info"`
		CosyVersion string `json:"cosyVersion"`
		IDEVersion  string `json:"ideVersion"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if payload.Version != "v1" || payload.CosyVersion != ideVersion || payload.IDEVersion != "" {
		t.Errorf("unexpected payload metadata: %+v", payload)
	}
	if payload.RequestID == "" || payload.Info == "" {
		t.Errorf("payload requestId/info must be non-empty: %+v", payload)
	}

	if got, want := h.Get("Cosy-Bodylength"), strconv.Itoa(len(body)); got != want {
		t.Errorf("Cosy-Bodylength = %q, want %q", got, want)
	}
	if got, want := h.Get("Cosy-Bodyhash"), fmt.Sprintf("%x", md5.Sum(body)); got != want {
		t.Errorf("Cosy-Bodyhash = %q, want %q", got, want)
	}
	if got, want := h.Get("Cosy-Sigpath"), "/api/v2/service/pro/sse/agent_chat_generation"; got != want {
		t.Errorf("Cosy-Sigpath = %q, want %q", got, want)
	}
	if got := h.Get("Cosy-User"); got != testCreds.UserID {
		t.Errorf("Cosy-User = %q, want %q", got, testCreds.UserID)
	}
	if got := h.Get("Cosy-Machineid"); got != testCreds.MachineID {
		t.Errorf("Cosy-Machineid = %q, want %q", got, testCreds.MachineID)
	}

	// RSA-1024 wrapped key: 128 bytes.
	keyBytes, err := base64.StdEncoding.DecodeString(h.Get("Cosy-Key"))
	if err != nil {
		t.Fatalf("Cosy-Key is not base64: %v", err)
	}
	if len(keyBytes) != 128 {
		t.Errorf("wrapped key length = %d, want 128", len(keyBytes))
	}

	// Parsing the URL ourselves must agree with the sig path.
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := h.Get("Cosy-Sigpath"), strings.TrimPrefix(u.Path, "/algo"); got != want {
		t.Errorf("Cosy-Sigpath = %q, want %q", got, want)
	}
}

func TestBuildCosyHeadersFreshNonces(t *testing.T) {
	body := []byte(`{}`)
	rawURL := ChatURL(testCreds.BaseURL)
	first, err := buildCosyHeaders(body, rawURL, testCreds)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildCosyHeaders(body, rawURL, testCreds)
	if err != nil {
		t.Fatal(err)
	}
	if first.Get("Authorization") == second.Get("Authorization") {
		t.Error("Authorization must change per request (fresh AES key/payload)")
	}
	if first.Get("X-Request-Id") == second.Get("X-Request-Id") {
		t.Error("X-Request-Id must be fresh per request")
	}
}

func TestBuildCosyHeadersSigPathWithoutAlgo(t *testing.T) {
	h, err := buildCosyHeaders(nil, "https://api3.qoder.sh/api/v2/model/list", testCreds)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := h.Get("Cosy-Sigpath"), "/api/v2/model/list"; got != want {
		t.Errorf("Cosy-Sigpath = %q, want %q", got, want)
	}
	if got := h.Get("Cosy-Bodylength"); got != "0" {
		t.Errorf("Cosy-Bodylength = %q, want 0", got)
	}
}

func TestBuildCosyHeadersRequiresIdentity(t *testing.T) {
	rawURL := ChatURL(testCreds.BaseURL)
	if _, err := buildCosyHeaders(nil, rawURL, Creds{AuthToken: "dt-x"}); err == nil {
		t.Error("expected error for empty user id")
	}
	if _, err := buildCosyHeaders(nil, rawURL, Creds{UserID: "u"}); err == nil {
		t.Error("expected error for empty auth token")
	}
}
