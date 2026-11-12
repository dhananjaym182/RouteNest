package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The browser tab icon must be a real file, not an inline data: URI: browsers
// do not all render a data: URI favicon, so the tab stayed blank while the
// in-page logos rendered. Every surface that can open in a browser has to
// serve one.
func TestSiteIconIsServedAsAFile(t *testing.T) {
	cases := []struct {
		path        string
		contentType string
	}{
		{"/admin/assets/favicon.ico", "image/x-icon"},
		{"/admin/assets/icon.svg", "image/svg+xml"},
		{"/admin/assets/icon.png", "image/png"},
		{"/admin/assets/icon-512.png", "image/png"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			adminStaticHandler(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("content type = %q, want %q", got, tc.contentType)
			}
			if recorder.Body.Len() == 0 {
				t.Error("icon body is empty")
			}
		})
	}
}

// Browsers ask for /favicon.ico at the server root for every tab, including
// pages that render no HTML of their own (the API root answers 404).
func TestRootFaviconIsServed(t *testing.T) {
	recorder := httptest.NewRecorder()
	faviconHandler(recorder, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/x-icon" {
		t.Errorf("content type = %q, want image/x-icon", got)
	}
	if recorder.Body.Len() == 0 {
		t.Error("favicon body is empty")
	}
}

// The admin head must link the served files instead of an inline data: URI.
func TestAdminHeadLinksServedIcons(t *testing.T) {
	head, err := adminUIFS.ReadFile("ui/head.html")
	if err != nil {
		t.Fatalf("read head: %v", err)
	}
	markup := string(head)
	if strings.Contains(markup, "data:image/svg+xml") {
		t.Error("head still carries an inline data: URI favicon")
	}
	for _, want := range []string{
		`<link rel="icon" href="/admin/assets/favicon.ico" sizes="any">`,
		`<link rel="icon" href="/admin/assets/icon.svg" type="image/svg+xml">`,
		`<link rel="apple-touch-icon" href="/admin/assets/icon.png">`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("head is missing %q", want)
		}
	}
	// Every linked icon must exist in the embedded assets.
	for _, name := range []string{"ui/assets/favicon.ico", "ui/assets/icon.svg", "ui/assets/icon.png"} {
		if _, err := adminUIFS.ReadFile(name); err != nil {
			t.Errorf("embedded icon %q is missing: %v", name, err)
		}
	}
}
