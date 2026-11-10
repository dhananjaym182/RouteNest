//go:build desktop

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopBindingsRemainEmbeddedOnly(t *testing.T) {
	for _, adminURL := range []string{"http://127.0.0.1:3457/admin/", "http://localhost:3457/admin/", "http://[::1]:3457/admin/"} {
		if got := desktopBindingOrigins(adminURL); got != "" {
			t.Fatalf("additional origins for %q = %q", adminURL, got)
		}
	}
}

func TestDesktopAssetMiddlewareRestrictsContent(t *testing.T) {
	handler := desktopAssetMiddleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, desktopAssetOrigin()+"/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("GET status = %d", recorder.Code)
	}
	for _, header := range []string{"Content-Security-Policy", "Cross-Origin-Resource-Policy", "Permissions-Policy", "X-Frame-Options", "Cache-Control"} {
		if recorder.Header().Get(header) == "" {
			t.Fatalf("security header %s missing", header)
		}
	}
	request = httptest.NewRequest(http.MethodPost, desktopAssetOrigin()+"/", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", recorder.Code)
	}
}

func TestDesktopAssetsRejectForeignOriginsAndAPIs(t *testing.T) {
	handler := desktopAssetMiddleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	for _, address := range []string{"http://127.0.0.1:3457/admin/", "http://wails.localhost.attacker.test/admin/", desktopAssetOrigin() + "/admin/api/auth/session"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, address, nil))
		if recorder.Code < 400 {
			t.Fatalf("untrusted request to %q returned %d", address, recorder.Code)
		}
	}
	for _, origin := range []string{"http://127.0.0.1:3457", "null", desktopAssetOrigin() + ".attacker.test", desktopAssetOrigin() + "/extra"} {
		request := httptest.NewRequest(http.MethodGet, desktopAssetOrigin()+"/admin/", nil)
		request.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("origin %q returned %d", origin, recorder.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, desktopAssetOrigin()+"/admin/", nil)
	request.Header.Set("Origin", desktopAssetOrigin())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("embedded console GET returned %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("setup CSP incorrectly replaced console CSP")
	}
}
