package buildinfo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func resetVersionState(t *testing.T, url string) func() {
	t.Helper()
	oldURL, oldState, oldApp, oldClient := versionCheckURL, versionState, AppVersion, HTTPClient
	versionState = &versionCheckCache{}
	versionCheckURL = url
	AppVersion = "v0.1.0-beta.1"
	HTTPClient = nil
	return func() {
		versionCheckURL = oldURL
		versionState = oldState
		AppVersion = oldApp
		HTTPClient = oldClient
	}
}

func releaseServer(body string, status int, hits *int32) (*httptest.Server, func(string, int)) {
	type response struct {
		body   string
		status int
	}
	var reply atomic.Pointer[response]
	setReply := func(body string, status int) {
		reply.Store(&response{body: body, status: status})
	}
	setReply(body, status)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		reply := reply.Load()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		_, _ = io.WriteString(w, reply.body)
	}))
	return server, setReply
}

func TestVersionCheckFetchAndCache(t *testing.T) {
	var hits int32
	srv, setReply := releaseServer(`{"tag_name":"v0.2.0","html_url":"u2","published_at":"p2"}`, http.StatusOK, &hits)
	defer srv.Close()
	defer resetVersionState(t, srv.URL)()

	versionState.refresh(false)
	if got := atomic.LoadInt32(&hits); got != 1 || versionState.latest != "v0.2.0" || versionState.err != "" {
		t.Fatalf("first refresh: hits=%d latest=%q err=%q", got, versionState.latest, versionState.err)
	}
	setReply(`{"tag_name":"v0.3.0"}`, http.StatusOK)
	versionState.refresh(false)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("cached refresh should not hit upstream, hits=%d", got)
	}
	versionState.refresh(true)
	if got := atomic.LoadInt32(&hits); got != 2 || versionState.latest != "v0.3.0" {
		t.Fatalf("forced refresh: hits=%d latest=%q", got, versionState.latest)
	}
	setReply(`{"tag_name":"v0.4.0"}`, http.StatusOK)
	versionState.checkedAt = time.Now().Add(-versionCheckTTL - time.Second)
	versionState.refresh(false)
	if got := atomic.LoadInt32(&hits); got != 3 || versionState.latest != "v0.4.0" {
		t.Fatalf("expired refresh: hits=%d latest=%q", got, versionState.latest)
	}
}

func TestVersionCheckErrorKeepsLastLatest(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
	}{
		{"invalid JSON", "boom", http.StatusOK},
		{"no stable release", `{"message":"Not Found"}`, http.StatusNotFound},
		{"rate limited", `{"message":"API rate limit exceeded"}`, http.StatusForbidden},
		{"upstream error", "", http.StatusInternalServerError},
		{"missing tag", `{}`, http.StatusOK},
		{"invalid tag", `{"tag_name":"v0.2.0.1"}`, http.StatusOK},
		{"incomplete tag", `{"tag_name":"v0.2"}`, http.StatusOK},
		{"draft", `{"tag_name":"v0.2.0","draft":true}`, http.StatusOK},
		{"prerelease flag", `{"tag_name":"v0.2.0","prerelease":true}`, http.StatusOK},
		{"prerelease tag", `{"tag_name":"v0.2.0-beta.1"}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv, setReply := releaseServer(`{"tag_name":"v0.1.0","html_url":"stable-url","published_at":"stable-date"}`, http.StatusOK, &hits)
			defer srv.Close()
			defer resetVersionState(t, srv.URL)()

			versionState.refresh(true)
			setReply(tc.body, tc.status)
			versionState.refresh(true)
			if versionState.latest != "v0.1.0" || versionState.htmlURL != "stable-url" || versionState.published != "stable-date" || versionState.err == "" {
				t.Fatalf("failed refresh should keep last stable result: %+v", versionState)
			}
			versionState.refresh(false)
			if got := atomic.LoadInt32(&hits); got != 2 {
				t.Fatalf("fetch errors should be cached, hits=%d", got)
			}
			setReply(`{"tag_name":"v0.2.0","html_url":"next-url","published_at":"next-date"}`, http.StatusOK)
			versionState.refresh(true)
			if versionState.latest != "v0.2.0" || versionState.htmlURL != "next-url" || versionState.published != "next-date" || versionState.err != "" {
				t.Fatalf("successful retry should replace result and clear error: %+v", versionState)
			}
		})
	}
}

func TestVersionSemverCompare(t *testing.T) {
	cases := []struct {
		name, current, latest string
		want                  bool
	}{
		{"patch increase", "v0.1.9", "v0.1.10", true},
		{"patch decrease", "v0.1.10", "v0.1.9", false},
		{"minor increase", "v0.1.99", "v0.2.0", true},
		{"minor decrease", "v0.2.0", "v0.1.99", false},
		{"major increase", "v0.99.99", "v1.0.0-alpha", true},
		{"major decrease", "v1.0.0-alpha", "v0.99.99", false},
		{"equal stable", "v0.1.0", "v0.1.0", false},
		{"unprefixed", "0.1.0", "0.2.0", true},
		{"tag normalization", " \tv0.1.0-beta.1\n", " 0.1.0 ", true},
		{"beta to stable", "v0.1.0-beta.1", "v0.1.0", true},
		{"stable to same beta", "v0.1.0", "v0.1.0-beta.1", false},
		{"beta increase", "v0.1.0-beta.1", "v0.1.0-beta.2", true},
		{"beta decrease", "v0.1.0-beta.2", "v0.1.0-beta.1", false},
		{"numeric beta increase", "v0.1.0-beta.2", "v0.1.0-beta.10", true},
		{"numeric beta decrease", "v0.1.0-beta.10", "v0.1.0-beta.2", false},
		{"beta to rc", "v0.1.0-beta.10", "v0.1.0-rc.1", true},
		{"rc to beta", "v0.1.0-rc.1", "v0.1.0-beta.10", false},
		{"equal preview", "v0.1.0-beta.1", "v0.1.0-beta.1", false},
		{"compact rc to stable", "v0.1.0-rc1", "v0.1.0", true},
		{"core outranks preview", "v0.1.0", "v0.2.0-alpha", true},
		{"older stable", "v0.2.0-alpha", "v0.1.0", false},
		{"numeric before alphabetic", "v0.1.0-9", "v0.1.0-A", true},
		{"alphabetic after numeric", "v0.1.0-A", "v0.1.0-9", false},
		{"zero before alphabetic", "v0.1.0-0", "v0.1.0-alpha", true},
		{"extra identifier", "v0.1.0-beta", "v0.1.0-beta.0", true},
		{"shorter preview", "v0.1.0-beta.0", "v0.1.0-beta", false},
		{"ASCII case", "v0.1.0-BETA", "v0.1.0-beta", true},
		{"ASCII case reverse", "v0.1.0-beta", "v0.1.0-BETA", false},
		{"ASCII numeric-looking text", "v0.1.0-beta2", "v0.1.0-beta10", false},
		{"ASCII hyphen", "v0.1.0-beta-1", "v0.1.0-beta1", true},
		{"nonnumeric leading zero", "v0.1.0-01a", "v0.1.0-1a", true},
		{"stable metadata ignored", "v0.1.0+build.1", "v0.1.0+build.2", false},
		{"metadata removal ignored", "v0.1.0+build.2", "v0.1.0", false},
		{"metadata addition ignored", "v0.1.0", "v0.1.0+build.2", false},
		{"preview metadata ignored", "v0.1.0-beta.1+build.1", "v0.1.0-beta.1+build.2", false},
		{"preview metadata to stable", "v0.1.0-beta.1+build.99", "v0.1.0+build.01", true},
		{"dev build", "dev", "v0.1.0", false},
		{"empty build", "", "v0.1.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionNewer(tc.current, tc.latest); got != tc.want {
				t.Errorf("versionNewer(%q, %q)=%v want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestVersionSemverPrereleasePrecedence(t *testing.T) {
	ordered := []string{
		"v0.1.0-alpha",
		"v0.1.0-alpha.1",
		"v0.1.0-alpha.beta",
		"v0.1.0-beta",
		"v0.1.0-beta.2",
		"v0.1.0-beta.11",
		"v0.1.0-rc.1",
		"v0.1.0",
	}
	for i, current := range ordered {
		for j, latest := range ordered {
			if got := versionNewer(current, latest); got != (j > i) {
				t.Errorf("versionNewer(%q, %q)=%v want %v", current, latest, got, j > i)
			}
		}
	}
}

func TestVersionSemverUnboundedNumbers(t *testing.T) {
	smaller, larger := strings.Repeat("9", 100), "1"+strings.Repeat("0", 100)
	pairs := [][2]string{
		{smaller + ".0.0", larger + ".0.0"},
		{"0." + smaller + ".0", "0." + larger + ".0"},
		{"0.1." + smaller, "0.1." + larger},
		{"0.1.0-beta." + smaller, "0.1.0-beta." + larger},
		{"0.1.0-beta." + larger, "0.1.0-beta.alpha"},
	}
	for _, pair := range pairs {
		if !versionNewer(pair[0], pair[1]) || versionNewer(pair[1], pair[0]) {
			t.Errorf("incorrect precedence for arbitrarily large identifiers: %q < %q", pair[0], pair[1])
		}
	}
}

func TestParseSemver(t *testing.T) {
	cases := []struct {
		input      string
		core       [3]string
		prerelease string
	}{
		{"0.0.0", [3]string{"0", "0", "0"}, ""},
		{"v0.1.0", [3]string{"0", "1", "0"}, ""},
		{" \tv0.1.0-beta.1+build.01\n", [3]string{"0", "1", "0"}, "beta.1"},
		{"0.1.0-0", [3]string{"0", "1", "0"}, "0"},
		{"0.1.0-01a", [3]string{"0", "1", "0"}, "01a"},
		{"0.1.0--", [3]string{"0", "1", "0"}, "-"},
		{"0.1.0-rc-1+build-01.00", [3]string{"0", "1", "0"}, "rc-1"},
		{"0.1.0+001", [3]string{"0", "1", "0"}, ""},
		{"0.1.0+-", [3]string{"0", "1", "0"}, ""},
		{"0.1.0+build.beta-1", [3]string{"0", "1", "0"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, ok := parseSemver(tc.input)
			if !ok || got.core != tc.core || strings.Join(got.prerelease, ".") != tc.prerelease {
				t.Errorf("parseSemver(%q)=%+v, %v want core=%v prerelease=%q", tc.input, got, ok, tc.core, tc.prerelease)
			}
		})
	}
}

func TestVersionSemverRejectsInvalidVersions(t *testing.T) {
	invalid := []string{
		"", " ", "dev", "v", "vdev", "0", "0.1", "v0.1", "0.1.0.1",
		".1.0", "0..0", "0.1.", "00.1.0", "0.01.0", "0.1.00",
		"-0.1.0", "0.-1.0", "0.1.-1", "+0.1.0", "0.+1.0", "0.1.+0",
		"V0.1.0", "vv0.1.0", "v 0.1.0", "release-0.1.0",
		"0. 1.0", "0.1.0 beta", "0.1.0\x00", "0.1.0\n-beta.1",
		"0.1.0-", "0.1.0+", "0.1.0-+build", "0.1.0-beta+",
		"0.1.0-01", "0.1.0-beta.01", "0.1.0-beta..1", "0.1.0-.beta", "0.1.0-beta.",
		"0.1.0-beta_1", "0.1.0-beta/1", "0.1.0-béta", "0.1.0-beta.１",
		"0.1.0+build..1", "0.1.0+.build", "0.1.0+build.", "0.1.0+build_1",
		"0.1.0+build+other", "0.1.0-beta+build+other", "0.1.0+build/1", "0.1.0+构建",
		"0.1.0+build\x00", "0.1.0-beta\t.1", "0.1.0-beta!", "0.1.0+build?",
	}
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			if got, ok := parseSemver(input); ok {
				t.Errorf("parseSemver(%q) accepted invalid version: %+v", input, got)
			}
			if versionNewer(input, "999.999.999") {
				t.Errorf("invalid current %q must not prompt an update", input)
			}
			if versionNewer("0.0.0", input) {
				t.Errorf("invalid latest %q must not prompt an update", input)
			}
		})
	}
}

func TestCurrentVersionStatusPayload(t *testing.T) {
	var hits int32
	srv, _ := releaseServer(`{"tag_name":" v0.1.0 ","html_url":"h","published_at":"2026-11-14T00:00:00Z","draft":false,"prerelease":false}`, http.StatusOK, &hits)
	defer srv.Close()
	defer resetVersionState(t, srv.URL)()

	st := CurrentVersionStatus(false)
	if st.Current != "v0.1.0-beta.1" || st.Latest != "v0.1.0" || !st.UpdateAvailable || st.ReleaseURL != "h" || st.PublishedAt != "2026-11-14T00:00:00Z" || st.CheckedAtMillis <= 0 || st.Error != "" {
		t.Fatalf("status mismatch: %+v", st)
	}
}

func TestCurrentVersionStatusStableReleasePolicy(t *testing.T) {
	cases := []struct {
		name, current, tag string
		draft, prerelease  bool
		status             int
		wantUpdate         bool
		wantError          bool
	}{
		{"preview to stable", "v0.1.0-beta.1", "v0.1.0", false, false, http.StatusOK, true, false},
		{"stable up to date", "v0.1.0", "v0.1.0", false, false, http.StatusOK, false, false},
		{"stable patch", "v0.1.0", "v0.1.1", false, false, http.StatusOK, true, false},
		{"old stable", "v0.2.0-beta.1", "v0.1.0", false, false, http.StatusOK, false, false},
		{"dev", "dev", "v0.1.0", false, false, http.StatusOK, false, false},
		{"invalid current", "v0.1", "v0.1.0", false, false, http.StatusOK, false, false},
		{"metadata equality", "v0.1.0+local", "v0.1.0+release.001", false, false, http.StatusOK, false, false},
		{"preview repository has no stable", "v0.1.0-beta.1", "", false, false, http.StatusNotFound, false, true},
		{"stable repository has no stable", "v0.1.0", "", false, false, http.StatusNotFound, false, true},
		{"reject flagged preview", "v0.1.0", "v0.2.0", false, true, http.StatusOK, false, true},
		{"reject preview tag", "v0.1.0-beta.1", "v0.2.0-beta.1", false, false, http.StatusOK, false, true},
		{"reject draft", "v0.1.0", "v0.2.0", true, false, http.StatusOK, false, true},
		{"reject malformed tag", "v0.1.0-beta.1", "v0.1.0.99", false, false, http.StatusOK, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(githubReleaseJSON{
				TagName: tc.tag, HTMLURL: "release-url", PublishedAt: "release-date",
				Draft: tc.draft, Prerelease: tc.prerelease,
			})
			if err != nil {
				t.Fatal(err)
			}
			var hits int32
			srv, _ := releaseServer(string(body), tc.status, &hits)
			defer srv.Close()
			defer resetVersionState(t, srv.URL)()
			AppVersion = tc.current

			st := CurrentVersionStatus(false)
			if st.UpdateAvailable != tc.wantUpdate || (st.Error != "") != tc.wantError || st.Current != tc.current || st.CheckedAtMillis <= 0 {
				t.Fatalf("unexpected status: %+v", st)
			}
			if tc.wantError {
				if st.Latest != "" || st.ReleaseURL != "" || st.PublishedAt != "" {
					t.Fatalf("invalid upstream result must not enter the cache: %+v", st)
				}
			} else if st.Latest != tc.tag || st.ReleaseURL != "release-url" || st.PublishedAt != "release-date" {
				t.Fatalf("missing release details: %+v", st)
			}
			_ = CurrentVersionStatus(false)
			if got := atomic.LoadInt32(&hits); got != 1 {
				t.Fatalf("cached status should use exactly one request, hits=%d", got)
			}
		})
	}
}

type versionCheckRoundTripFunc func(*http.Request) (*http.Response, error)

func (f versionCheckRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestVersionCheckUsesStableLatestEndpoint(t *testing.T) {
	const endpoint = "https://api.github.com/repos/dhananjaym182/RouteNest/releases/latest"
	if versionCheckURL != endpoint {
		t.Fatalf("default endpoint = %q, want stable /releases/latest", versionCheckURL)
	}
	defer resetVersionState(t, endpoint)()
	hits := 0
	HTTPClient = &http.Client{Transport: versionCheckRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		hits++
		if req.Method != http.MethodGet || req.URL.String() != endpoint || req.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("unexpected release request: %s %s headers=%v", req.Method, req.URL, req.Header)
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Error("release request should have a timeout")
		}
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`))}, nil
	})}
	st := CurrentVersionStatus(true)
	if st.UpdateAvailable || st.Latest != "" || st.Error == "" || hits != 1 {
		t.Fatalf("no stable release must not trigger a preview fallback: hits=%d status=%+v", hits, st)
	}
}
