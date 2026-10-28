package main

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
	old, had := providerDeviceFlows[provider]
	providerDeviceFlows[provider] = spec
	return func() {
		if had {
			providerDeviceFlows[provider] = old
		} else {
			delete(providerDeviceFlows, provider)
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
		loadConnections()
		connectionsMu.Lock()
		n := len(connections)
		var c Connection
		if n > 0 {
			c = *connections[0]
		}
		connectionsMu.Unlock()
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
		c1 := upsertConnection(&Connection{Provider: "kilocode", Email: "a@b.c", AccessToken: "t1", Status: "active"})
		c2 := upsertConnection(&Connection{Provider: "kilocode", Email: "a@b.c", AccessToken: "t2", Status: "active"})
		if c1.ID != c2.ID {
			t.Errorf("dedup by email failed: %s vs %s", c1.ID, c2.ID)
		}
		connectionsMu.Lock()
		n := len(connections)
		tok := connections[0].AccessToken
		connectionsMu.Unlock()
		if n != 1 || tok != "t2" {
			t.Errorf("want single rotated connection, got n=%d tok=%s", n, tok)
		}
	})
}

// 派发集成：绑定连接的 provider 用连接 token + 动态组织头；401 → 标重登并落选。
func TestCallProviderUsesConnectionAndMarksReauth(t *testing.T) {
	var gotAuth, gotOrg string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotOrg = r.Header.Get("X-Test-Org")
		if gotAuth == "Bearer expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()

	withTestConnections(t, func() {
		conn := upsertConnection(&Connection{Provider: "t-hdr", Email: "u@x.y", AccessToken: "good", Status: "active",
			ProviderSpecificData: map[string]string{"orgId": "org-7"}})
		restore := setTestFlow("t-hdr", deviceFlowSpec{OrgHeader: "X-Test-Org", IntervalSec: 1})
		defer restore()
		p := &CustomProvider{ID: "p1", Name: "ConnProv", BaseURL: upstream.URL, Enabled: true, ConnectionID: conn.ID, ModelIDs: []string{"t/model"}}
		oldProvs, oldLoaded := customProviders, providersLoaded
		defer func() { customProviders, providersLoaded = oldProvs, oldLoaded }()
		customProviders = []*CustomProvider{p}
		providersLoaded = true

		resp, err := callProvider(p, map[string]any{"model": "t/model", "messages": []any{}}, false)
		if err != nil {
			t.Fatalf("callProvider: %v", err)
		}
		resp.Body.Close()
		if gotAuth != "Bearer good" || gotOrg != "org-7" {
			t.Errorf("connection creds not injected: auth=%q org=%q", gotAuth, gotOrg)
		}

		// 过期连接 → 该 provider 不入候选
		connectionsMu.Lock()
		conn.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
		connectionsMu.Unlock()
		if got := resolveProvidersForModel("t/model"); len(got) != 0 {
			t.Errorf("expired connection provider must be filtered from selection, got %d", len(got))
		}
		connectionsMu.Lock()
		st := connections[0].Status
		connectionsMu.Unlock()
		if st != "reauth_required" {
			t.Errorf("expired no-refresh connection should be reauth_required, got %s", st)
		}

		// 401 → markConnectionReauth：还原可用 token 但上游故意拒绝
		conn2 := upsertConnection(&Connection{Provider: "t-hdr", Email: "u@x.y", AccessToken: "expired", Status: "active"})
		p.ConnectionID = conn2.ID
		_, err = callProvider(p, map[string]any{"model": "t/model", "messages": []any{}}, false)
		if err == nil {
			t.Fatal("401 should surface as error")
		}
		connectionsMu.Lock()
		var st2 string
		for _, c := range connections {
			if c.ID == conn2.ID {
				st2 = c.Status
			}
		}
		connectionsMu.Unlock()
		if st2 != "reauth_required" {
			t.Errorf("401 from upstream should mark connection reauth_required, got %s", st2)
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

// admin 处理器闭环：import → list（token 不外泄）→ delete。
func TestConnectionAdminHandlers(t *testing.T) {
	withTestConnections(t, func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/admin/api/connections/import",
			strings.NewReader(`{"provider":"kilocode","accessToken":"sekret-token","email":"h@x.io","orgId":"org-5"}`))
		handleConnectionImport(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("import code %d body %s", rec.Code, rec.Body.String())
		}

		rec = httptest.NewRecorder()
		handleConnectionsList(rec, httptest.NewRequest("GET", "/admin/api/connections", nil))
		var lr struct {
			Data struct {
				Connections []map[string]any `json:"connections"`
				Flows       []string         `json:"flows"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil {
			t.Fatal(err)
		}
		if len(lr.Data.Connections) != 1 {
			t.Fatalf("want 1 connection, got %d", len(lr.Data.Connections))
		}
		c := lr.Data.Connections[0]
		if c["accessToken"] == "sekret-token" || c["accessToken"] != "" {
			t.Errorf("list must redact accessToken, got %v", c["accessToken"])
		}
		if c["status"] != "active" {
			t.Errorf("status %v", c["status"])
		}
		if psd, _ := c["providerSpecificData"].(map[string]any); psd == nil || psd["orgId"] != "org-5" {
			t.Errorf("orgId not persisted: %v", c["providerSpecificData"])
		}
		if len(lr.Data.Flows) == 0 {
			t.Errorf("flows should list registered device flows (kilocode)")
		}

		id, _ := c["id"].(string)
		rec = httptest.NewRecorder()
		handleConnectionDelete(rec, httptest.NewRequest("POST", "/admin/api/connections/delete",
			strings.NewReader(`{"id":"`+id+`"}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("delete code %d", rec.Code)
		}
		connectionsMu.Lock()
		n := len(connections)
		connectionsMu.Unlock()
		if n != 0 {
			t.Errorf("connection not removed, n=%d", n)
		}
	})
}

// RFC 8628 模式重放：pending×2 → 成功（带 refresh/expires_in），PKCE challenge/verifier 成对，
// profile 补 login 为邮箱。
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
		connectionsMu.Lock()
		c := *connections[0]
		connectionsMu.Unlock()
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

// 二段兑换（Copilot 作业票）：connectionBearer 首次兑换并缓存，临期自动重换。
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
		conn := upsertConnection(&Connection{Provider: "t-ex", AccessToken: "oauth-master", Status: "active"})
		if got := connectionBearer(conn, spec); got != "job-1" {
			t.Fatalf("want job-1 got %s", got)
		}
		if got := connectionBearer(conn, spec); got != "job-1" {
			t.Fatalf("cached bearer should be reused, got %s", got)
		}
		if atomic.LoadInt32(&hits) != 1 {
			t.Errorf("second call must not re-exchange")
		}
		// 强制临期 → 重新兑换
		updateConnectionPSD(conn.ID, map[string]string{"exchangedExp": "1"})
		if got := connectionBearer(conn, spec); got != "job-2" {
			t.Fatalf("expired job token should renew, got %s", got)
		}
		connectionsMu.Lock()
		var persisted *Connection
		for _, c := range connections {
			if c.ID == conn.ID {
				persisted = c
			}
		}
		psd := persisted.ProviderSpecificData["exchangedToken"]
		connectionsMu.Unlock()
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
		connectionsMu.Lock()
		c := *connections[0]
		connectionsMu.Unlock()
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

type batchImportResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Total    int `json:"total"`
		Imported int `json:"imported"`
		Failed   int `json:"failed"`
		Results  []struct {
			Index    int    `json:"index"`
			OK       bool   `json:"ok"`
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Email    string `json:"email"`
			Error    string `json:"error"`
		} `json:"results"`
	} `json:"data"`
}

func postImportBatch(t *testing.T, body string) (*httptest.ResponseRecorder, batchImportResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	handleConnectionImportBatch(rec, httptest.NewRequest("POST", "/admin/api/connections/import-batch", strings.NewReader(body)))
	var out batchImportResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode batch response: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec, out
}

// 混合批次：2 条有效（不同 provider）+ 缺 token + 缺 provider，失败项不中断整批。
func TestConnectionImportBatchMixed(t *testing.T) {
	isolateConnections(t)
	rec, out := postImportBatch(t, `{"items":[
		{"provider":"kilocode","accessToken":"tok-a","email":"a@b.c"},
		{"provider":"qoder","accessToken":"tok-b","email":"q@b.c"},
		{"provider":"kilocode","email":"missing-token@b.c"},
		{"accessToken":"tok-d","email":"missing-provider@b.c"}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch code %d body %s", rec.Code, rec.Body.String())
	}
	if !out.Success {
		t.Errorf("success flag false: %s", rec.Body.String())
	}
	if out.Data.Total != 4 || out.Data.Imported != 2 || out.Data.Failed != 2 {
		t.Errorf("counts wrong: total=%d imported=%d failed=%d", out.Data.Total, out.Data.Imported, out.Data.Failed)
	}
	if len(out.Data.Results) != 4 {
		t.Fatalf("want 4 results got %d", len(out.Data.Results))
	}
	for i, r := range out.Data.Results {
		if r.Index != i {
			t.Errorf("result %d: index %d", i, r.Index)
		}
	}
	for _, i := range []int{0, 1} {
		r := out.Data.Results[i]
		if !r.OK || r.ID == "" || r.Error != "" {
			t.Errorf("result %d should be ok: %+v", i, r)
		}
	}
	if out.Data.Results[0].Provider != "kilocode" || out.Data.Results[0].Email != "a@b.c" {
		t.Errorf("result 0 wrong: %+v", out.Data.Results[0])
	}
	if out.Data.Results[1].Provider != "qoder" || out.Data.Results[1].Email != "q@b.c" {
		t.Errorf("result 1 wrong: %+v", out.Data.Results[1])
	}
	if r := out.Data.Results[2]; r.OK || r.ID != "" || r.Provider != "kilocode" || r.Error != "provider and accessToken required" {
		t.Errorf("missing token result wrong: %+v", r)
	}
	if r := out.Data.Results[3]; r.OK || r.ID != "" || r.Provider != "" || r.Error != "provider and accessToken required" {
		t.Errorf("missing provider result wrong: %+v", r)
	}
	connectionsMu.Lock()
	n := len(connections)
	connectionsMu.Unlock()
	if n != 2 {
		t.Errorf("want 2 persisted connections, got %d", n)
	}
}

// 同一批内 provider+email 重复 → 两条都 ok、同一连接 id，列表只增 1。
func TestConnectionImportBatchDedup(t *testing.T) {
	isolateConnections(t)
	rec, out := postImportBatch(t, `{"items":[
		{"provider":"kilocode","accessToken":"dup-t1","email":"dup@b.c"},
		{"provider":"kilocode","accessToken":"dup-t2","email":"dup@b.c"}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch code %d body %s", rec.Code, rec.Body.String())
	}
	if out.Data.Imported != 2 || out.Data.Failed != 0 {
		t.Fatalf("counts wrong: %+v", out.Data)
	}
	if len(out.Data.Results) != 2 || !out.Data.Results[0].OK || !out.Data.Results[1].OK {
		t.Fatalf("both items should be ok: %+v", out.Data.Results)
	}
	if out.Data.Results[0].ID == "" || out.Data.Results[0].ID != out.Data.Results[1].ID {
		t.Errorf("dedup should return same connection id: %q vs %q",
			out.Data.Results[0].ID, out.Data.Results[1].ID)
	}
	connectionsMu.Lock()
	n := len(connections)
	tok := ""
	if n > 0 {
		tok = connections[0].AccessToken
	}
	connectionsMu.Unlock()
	if n != 1 {
		t.Errorf("dedup should add exactly 1 connection, got %d", n)
	}
	if tok != "dup-t2" {
		t.Errorf("last token should win, got %q", tok)
	}
}

// orgId（trim 后）写入 ProviderSpecificData，且 provider/accessToken 走共享语义。
func TestConnectionImportBatchPersistsOrgID(t *testing.T) {
	isolateConnections(t)
	rec, out := postImportBatch(t, `{"items":[{"provider":" kilocode ","accessToken":" tok-org ","email":" o@b.c ","orgId":" org-42 "}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch code %d body %s", rec.Code, rec.Body.String())
	}
	if out.Data.Imported != 1 || len(out.Data.Results) != 1 || !out.Data.Results[0].OK {
		t.Fatalf("item should be imported: %+v", out.Data)
	}
	id := out.Data.Results[0].ID
	connectionsMu.Lock()
	var c *Connection
	for _, cur := range connections {
		if cur.ID == id {
			cp := *cur
			c = &cp
			break
		}
	}
	connectionsMu.Unlock()
	if c == nil {
		t.Fatalf("connection %s not persisted", id)
	}
	if c.ProviderSpecificData["orgId"] != "org-42" {
		t.Errorf("orgId not persisted: %+v", c.ProviderSpecificData)
	}
	if c.Provider != "kilocode" || c.Email != "o@b.c" || c.AccessToken != "tok-org" {
		t.Errorf("trimmed fields wrong: %+v", c)
	}
	if c.AuthType != "import_token" || c.Status != "active" {
		t.Errorf("authType/status wrong: %+v", c)
	}
}

// 空/缺失 items 与超上限 → 400。
func TestConnectionImportBatchValidation(t *testing.T) {
	isolateConnections(t)
	for _, body := range []string{`{"items":[]}`, `{}`, `{"items":null}`, `not-json`} {
		rec, _ := postImportBatch(t, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: want 400 got %d", body, rec.Code)
			continue
		}
		var er struct {
			Error string `json:"error"`
		}
		json.Unmarshal(rec.Body.Bytes(), &er)
		if er.Error != "items required" {
			t.Errorf("body %q: want items required, got %q", body, er.Error)
		}
	}

	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 501; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"provider":"kilocode","accessToken":"t"}`)
	}
	b.WriteString(`]}`)
	rec, _ := postImportBatch(t, b.String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("501 items: want 400 got %d", rec.Code)
	}
	var er struct {
		Error string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error != "too many items (max 500)" {
		t.Errorf("501 items error = %q", er.Error)
	}
}

// 响应体不得包含 accessToken / tokenFp 字段名或任何 token / 指纹值。
func TestConnectionImportBatchRedactsTokens(t *testing.T) {
	isolateConnections(t)
	rec, _ := postImportBatch(t, `{"items":[
		{"provider":"kilocode","accessToken":"super-secret-token","email":"s@b.c"},
		{"provider":"kilocode","accessToken":"no-email-secret"}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch code %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, bad := range []string{"accessToken", "tokenFp", "super-secret-token", "no-email-secret"} {
		if strings.Contains(body, bad) {
			t.Errorf("response leaks %q: %s", bad, body)
		}
	}
	connectionsMu.Lock()
	fp := ""
	for _, c := range connections {
		if c.ProviderSpecificData["tokenFp"] != "" {
			fp = c.ProviderSpecificData["tokenFp"]
		}
	}
	connectionsMu.Unlock()
	if fp == "" {
		t.Fatal("expected a token fingerprint for the email-less item")
	}
	if strings.Contains(body, fp) {
		t.Errorf("response leaks token fingerprint %q: %s", fp, body)
	}
}

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
