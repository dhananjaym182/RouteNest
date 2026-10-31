package connections

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withTestConnections 隔离全局连接表并在结束后还原。
func withTestConnections(t *testing.T, fn func()) {
	old, oldLoaded := connections, connectionsLoaded
	defer func() { connections, connectionsLoaded = old, oldLoaded }()
	connections = nil
	connectionsLoaded = true
	fn()
}

func setTestFlow(provider string, spec deviceFlowSpec) func() {
	old, had := ProviderDeviceFlows[provider]
	ProviderDeviceFlows[provider] = spec
	return func() {
		if had {
			ProviderDeviceFlows[provider] = old
		} else {
			delete(ProviderDeviceFlows, provider)
		}
	}
}

// §8.3.6：设备码流程重放 —— 202 pending → approved（带 token/userEmail），
// profile 补 orgId，落库连接无 refreshToken（Kilo 语义）。
func TestProviderDeviceLoginApproved(t *testing.T) {
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/codes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": "USER-1", "verificationUrl": "http://example.test/verify", "expiresIn": 10})
	})
	mux.HandleFunc("/codes/USER-1", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&polls, 1) < 3 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "approved", "token": "tok-abc", "userEmail": "dev@example.com"})
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		if r.Header.Get("Authorization") != "Bearer tok-abc" {
			t.Errorf("profile called without token")
		}
		json.NewEncoder(w).Encode(map[string]any{"organizations": []any{map[string]any{"id": "org-9"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	restore := setTestFlow("t-kilo", deviceFlowSpec{
		InitiateURL: srv.URL + "/codes", PollURLBase: srv.URL + "/codes",
		ProfileURL: srv.URL + "/profile", OrgIDPath: "organizations.0.id",
		OrgHeader: "X-Kilocode-OrganizationID", IntervalSec: 1,
	})
	defer restore()

	withTestConnections(t, func() {
		sess, err := startProviderDeviceLogin("t-kilo")
		if err != nil {
			t.Fatal(err)
		}
		if sess.UserCode != "USER-1" || !strings.Contains(sess.AuthURL, "verify") {
			t.Errorf("bad session: %+v", sess)
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			providerLoginMu.Lock()
			done := sess.Done
			providerLoginMu.Unlock()
			if done {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		providerLoginMu.Lock()
		ok, serr, conn := sess.Success, sess.Error, sess.Connection
		providerLoginMu.Unlock()
		if !ok {
			t.Fatalf("login failed: %s", serr)
		}
		LoadConnections()
		ConnectionsMu.Lock()
		n := len(connections)
		var c Connection
		if n > 0 {
			c = *connections[0]
		}
		ConnectionsMu.Unlock()
		if n != 1 {
			t.Fatalf("want 1 connection got %d", n)
		}
		if c.AccessToken != "tok-abc" || c.Email != "dev@example.com" || c.RefreshToken != "" || c.Status != "active" {
			t.Errorf("connection wrong: %+v", c)
		}
		if c.ProviderSpecificData["orgId"] != "org-9" {
			t.Errorf("orgId not captured (dynamic header source): %+v", c.ProviderSpecificData)
		}
		if conn == nil {
			t.Errorf("session missing connection")
		}
	})
}

// 403 → denied、410 → expired 语义。
func TestProviderDeviceLoginDeniedAndExpired(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{{http.StatusForbidden, "denied"}, {http.StatusGone, "expired"}} {
		mux := http.NewServeMux()
		mux.HandleFunc("/codes", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"code": "C1", "verificationUrl": "u", "expiresIn": 30})
		})
		mux.HandleFunc("/codes/C1", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) })
		srv := httptest.NewServer(mux)
		restore := setTestFlow("t-"+tc.want, deviceFlowSpec{InitiateURL: srv.URL + "/codes", PollURLBase: srv.URL + "/codes", IntervalSec: 1})
		withTestConnections(t, func() {
			sess, err := startProviderDeviceLogin("t-" + tc.want)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				providerLoginMu.Lock()
				done := sess.Done
				providerLoginMu.Unlock()
				if done {
					break
				}
				time.Sleep(150 * time.Millisecond)
			}
			providerLoginMu.Lock()
			ok, serr := sess.Success, sess.Error
			providerLoginMu.Unlock()
			if ok || !strings.Contains(strings.ToLower(serr), tc.want) {
				t.Errorf("status %d: want %s error, got ok=%v err=%q", tc.status, tc.want, ok, serr)
			}
		})
		restore()
		srv.Close()
	}
}

// provider+email 去重：同邮箱重登替换而不是叠加。
func TestConnectionDedupByEmail(t *testing.T) {
	withTestConnections(t, func() {
		c1 := UpsertConnection(&Connection{Provider: "kilocode", Email: "a@b.c", AccessToken: "t1", Status: "active"})
		c2 := UpsertConnection(&Connection{Provider: "kilocode", Email: "a@b.c", AccessToken: "t2", Status: "active"})
		if c1.ID != c2.ID {
			t.Errorf("dedup by email failed: %s vs %s", c1.ID, c2.ID)
		}
		ConnectionsMu.Lock()
		n := len(connections)
		tok := connections[0].AccessToken
		ConnectionsMu.Unlock()
		if n != 1 || tok != "t2" {
			t.Errorf("want single rotated connection, got n=%d tok=%s", n, tok)
		}
	})
}

// jsonPathString 支持数组下标路径（organizations.0.id）。
func TestJSONPathString(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(`{"organizations":[{"id":"o1"},{"id":"o2"}],"email":"e@x"}`), &m)
	if got := jsonPathString(m, "organizations.1.id"); got != "o2" {
		t.Errorf("got %q", got)
	}
	if got := jsonPathString(m, "email"); got != "e@x" {
		t.Errorf("got %q", got)
	}
	if got := jsonPathString(m, "organizations.5.id"); got != "" {
		t.Errorf("out of range should be empty, got %q", got)
	}
}

func TestRFC8628DeviceLogin(t *testing.T) {
	var mu sync.Mutex
	var polls int32
	var initiateBody, lastPollBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		initiateBody = string(b)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"device_code": "D1", "user_code": "USER-9",
			"verification_uri": "http://verify.test/login", "expires_in": 60})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		lastPollBody = string(b)
		mu.Unlock()
		if atomic.AddInt32(&polls, 1) < 3 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600})
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			t.Errorf("profile without new token: %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{"login": "octo"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	restore := setTestFlow("t-rfc", deviceFlowSpec{Mode: "rfc8628",
		InitiateURL: srv.URL + "/device/code", TokenURL: srv.URL + "/token",
		ClientID: "cid", Scope: "read:user", PKCE: true,
		ProfileURL: srv.URL + "/profile", EmailPath: "login", IntervalSec: 1})
	defer restore()

	withTestConnections(t, func() {
		sess, err := startProviderDeviceLogin("t-rfc")
		if err != nil {
			t.Fatal(err)
		}
		if sess.UserCode != "USER-9" || sess.AuthURL != "http://verify.test/login" {
			t.Errorf("bad session: %+v", sess)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			providerLoginMu.Lock()
			done := sess.Done
			providerLoginMu.Unlock()
			if done {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		providerLoginMu.Lock()
		ok, serr := sess.Success, sess.Error
		providerLoginMu.Unlock()
		if !ok {
			t.Fatalf("login failed: %s", serr)
		}
		mu.Lock()
		ib, pb := initiateBody, lastPollBody
		mu.Unlock()
		if !strings.Contains(ib, "code_challenge=") || !strings.Contains(ib, "scope=read%3Auser") {
			t.Errorf("initiate form wrong: %q", ib)
		}
		if !strings.Contains(pb, "code_verifier=") || !strings.Contains(pb, "grant_type=urn") {
			t.Errorf("poll form wrong: %q", pb)
		}
		ConnectionsMu.Lock()
		c := *connections[0]
		ConnectionsMu.Unlock()
		if c.AccessToken != "at-1" || c.RefreshToken != "rt-1" || c.Email != "octo" || c.ExpiresAt <= time.Now().UnixMilli() {
			t.Errorf("connection wrong: %+v", c)
		}
	})
}

// RFC 8628 拒绝/过期语义 → 会话报错。
func TestRFC8628Denied(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"device_code": "D1", "user_code": "U1", "verification_uri": "u", "expires_in": 60})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	restore := setTestFlow("t-rfc-denied", deviceFlowSpec{Mode: "rfc8628",
		InitiateURL: srv.URL + "/device/code", TokenURL: srv.URL + "/token", ClientID: "c", IntervalSec: 1})
	defer restore()
	withTestConnections(t, func() {
		sess, err := startProviderDeviceLogin("t-rfc-denied")
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			providerLoginMu.Lock()
			done := sess.Done
			providerLoginMu.Unlock()
			if done {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		providerLoginMu.Lock()
		ok, serr := sess.Success, sess.Error
		providerLoginMu.Unlock()
		if ok || !strings.Contains(serr, "denied") {
			t.Errorf("want denied error, got ok=%v err=%q", ok, serr)
		}
	})
}

// 二段兑换（Copilot 作业票）：ConnectionBearer 首次兑换并缓存，临期自动重换。
func TestConnectionBearerExchangeCachesAndRenews(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-master" {
			t.Errorf("exchange without master token")
		}
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"token": fmt.Sprintf("job-%d", n), "expires_at": time.Now().Add(5 * time.Minute).Unix()})
	}))
	defer srv.Close()
	spec := deviceFlowSpec{ExchangeURL: srv.URL, ExchangeTokenPath: "token", ExchangeExpPath: "expires_at"}
	withTestConnections(t, func() {
		conn := UpsertConnection(&Connection{Provider: "t-ex", AccessToken: "oauth-master", Status: "active"})
		if got := ConnectionBearer(conn, spec); got != "job-1" {
			t.Fatalf("want job-1 got %s", got)
		}
		if got := ConnectionBearer(conn, spec); got != "job-1" {
			t.Fatalf("cached bearer should be reused, got %s", got)
		}
		if atomic.LoadInt32(&hits) != 1 {
			t.Errorf("second call must not re-exchange")
		}
		// 强制临期 → 重新兑换
		updateConnectionPSD(conn.ID, map[string]string{"exchangedExp": "1"})
		if got := ConnectionBearer(conn, spec); got != "job-2" {
			t.Fatalf("expired job token should renew, got %s", got)
		}
		ConnectionsMu.Lock()
		var persisted *Connection
		for _, c := range connections {
			if c.ID == conn.ID {
				persisted = c
			}
		}
		psd := persisted.ProviderSpecificData["exchangedToken"]
		ConnectionsMu.Unlock()
		if psd != "job-2" {
			t.Errorf("renewed token not persisted: %s", psd)
		}
	})
}

// Qoder：本地 PKCE + nonce + machine_id → 浏览器授权 → GET 轮询（202 pending →
// 200 token）→ userinfo 补 email/org；machineId/userId 落 ProviderSpecificData。
func TestQoderDeviceLogin(t *testing.T) {
	var mu sync.Mutex
	var polls int32
	var pollQuery url.Values
	var pollUA string
	mux := http.NewServeMux()
	mux.HandleFunc("/deviceToken/poll", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pollQuery, pollUA = r.URL.Query(), r.Header.Get("User-Agent")
		mu.Unlock()
		if atomic.AddInt32(&polls, 1) < 3 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"token": "dt-abc", "refresh_token": "rt-q", "user_id": "u42",
			"expires_in": 0,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dt-abc" {
			t.Errorf("userinfo auth wrong: %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{"email": "q@example.test", "organization_id": "org-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	restore := setTestFlow("t-qoder", deviceFlowSpec{Mode: "qoder",
		LoginURL: "http://login.test/selectAccounts", TokenURL: srv.URL + "/deviceToken/poll",
		ProfileURL: srv.URL + "/userinfo", EmailPath: "email", OrgIDPath: "organization_id",
		UserAgent: "Go-http-client/2.0", IntervalSec: 1})
	defer restore()

	withTestConnections(t, func() {
		sess, err := startProviderDeviceLogin("t-qoder")
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(sess.AuthURL)
		if err != nil || u.Host != "login.test" {
			t.Fatalf("auth url wrong: %q (%v)", sess.AuthURL, err)
		}
		q := u.Query()
		for _, k := range []string{"challenge", "machine_id", "nonce"} {
			if q.Get(k) == "" {
				t.Errorf("auth url missing %s: %q", k, sess.AuthURL)
			}
		}
		if q.Get("challenge_method") != "S256" {
			t.Errorf("challenge_method = %q", q.Get("challenge_method"))
		}
		if sess.UserCode == "" || len(sess.UserCode) > 8 {
			t.Errorf("user code shape wrong: %q", sess.UserCode)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			providerLoginMu.Lock()
			done := sess.Done
			providerLoginMu.Unlock()
			if done {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		providerLoginMu.Lock()
		ok, serr := sess.Success, sess.Error
		providerLoginMu.Unlock()
		if !ok {
			t.Fatalf("login failed: %s", serr)
		}
		mu.Lock()
		pq, ua, nonce := pollQuery, pollUA, q.Get("nonce")
		mu.Unlock()
		if pq.Get("nonce") != nonce || pq.Get("verifier") == "" || pq.Get("challenge_method") != "S256" {
			t.Errorf("poll query wrong: %v", pq)
		}
		if ua != "Go-http-client/2.0" {
			t.Errorf("poll UA = %q", ua)
		}
		ConnectionsMu.Lock()
		c := *connections[0]
		ConnectionsMu.Unlock()
		if c.AccessToken != "dt-abc" || c.RefreshToken != "rt-q" || c.Email != "q@example.test" {
			t.Errorf("connection wrong: %+v", c)
		}
		if c.ExpiresAt <= time.Now().Add(23*time.Hour).UnixMilli() {
			t.Errorf("expired/short lifetime should be floored to >= 1 day, got %d", c.ExpiresAt)
		}
		if c.ProviderSpecificData["orgId"] != "org-1" || c.ProviderSpecificData["userId"] != "u42" ||
			c.ProviderSpecificData["machineId"] == "" || c.ProviderSpecificData["authMethod"] != "device" {
			t.Errorf("psd wrong: %+v", c.ProviderSpecificData)
		}
	})
}

// ---------- 批量导入（admin UI 批量 tab 契约） ----------

func TestQoderExpiryMs(t *testing.T) {
	if got := qoderExpiryMs(map[string]any{"expires_in": float64(0)}); got < time.Now().Add(23*time.Hour).UnixMilli() {
		t.Errorf("expired lifetime should be floored to >= 1 day, got %d", got)
	}
	baseMs := time.Now().Add(10 * 24 * time.Hour).UnixMilli()
	sec := baseMs / 1000
	if got := qoderExpiryMs(map[string]any{"expires_at": float64(sec)}); got != sec*1000 {
		t.Errorf("seconds epoch: got %d", got)
	}
	if got := qoderExpiryMs(map[string]any{"expires_at": float64(sec * 1000)}); got != sec*1000 {
		t.Errorf("millis epoch: got %d", got)
	}
	futureRFC := time.Now().Add(10 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if got, want := qoderExpiryMs(map[string]any{"expires_at": futureRFC}), sec*1000; got < want-2000 || got > want+2000 {
		t.Errorf("RFC3339 parse: got %d want ~%d", got, want)
	}
	if got := qoderExpiryMs(map[string]any{}); got <= time.Now().UnixMilli() {
		t.Errorf("default 30d expected, got %d", got)
	}
}
