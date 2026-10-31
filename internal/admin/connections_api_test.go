package admin

// HTTP contract tests for the connection adapters: import, list, delete, and
// batch import. Behaviour is tested in internal/connections; these tests pin the
// status codes and envelopes the admin console depends on.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/connections"
	"routenest/internal/deviceflow"
)

// clearAdminConnections removes every stored connection, so each test starts from
// an empty list.
func clearAdminConnections() {
	for _, c := range connections.ListConnections() {
		connections.RemoveConnection(c.ID)
	}
}

// isolateAdminConnections starts from an empty connection list and clears it again
// when the test ends.
func isolateAdminConnections(t *testing.T) {
	t.Helper()
	clearAdminConnections()
	t.Cleanup(clearAdminConnections)
}

// withAdminConnections runs fn against an empty connection list.
func withAdminConnections(t *testing.T, fn func()) {
	t.Helper()
	isolateAdminConnections(t)
	fn()
}

// admin 处理器闭环：import → list（token 不外泄）→ delete。
func TestConnectionAdminHandlers(t *testing.T) {
	withAdminConnections(t, func() {
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
		n := len(connections.ListConnections())
		if n != 0 {
			t.Errorf("connection not removed, n=%d", n)
		}
	})
}

// RFC 8628 模式重放：pending×2 → 成功（带 refresh/expires_in），PKCE challenge/verifier 成对，
// profile 补 login 为邮箱。

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
	isolateAdminConnections(t)
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
	n := len(connections.ListConnections())
	if n != 2 {
		t.Errorf("want 2 persisted connections, got %d", n)
	}
}

// 同一批内 provider+email 重复 → 两条都 ok、同一连接 id，列表只增 1。
func TestConnectionImportBatchDedup(t *testing.T) {
	isolateAdminConnections(t)
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
	// The stored token is not visible through the owner list; "last token wins" is
	// pinned by the connections package tests.
	if n := len(connections.ListConnections()); n != 1 {
		t.Errorf("dedup should add exactly 1 connection, got %d", n)
	}
}

// orgId（trim 后）写入 ProviderSpecificData，且 provider/accessToken 走共享语义。
func TestConnectionImportBatchPersistsOrgID(t *testing.T) {
	isolateAdminConnections(t)
	rec, out := postImportBatch(t, `{"items":[{"provider":" kilocode ","accessToken":" tok-org ","email":" o@b.c ","orgId":" org-42 "}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch code %d body %s", rec.Code, rec.Body.String())
	}
	if out.Data.Imported != 1 || len(out.Data.Results) != 1 || !out.Data.Results[0].OK {
		t.Fatalf("item should be imported: %+v", out.Data)
	}
	id := out.Data.Results[0].ID
	var c *connections.Connection
	for _, cur := range connections.ListConnections() {
		if cur.ID == id {
			cp := cur
			c = &cp
			break
		}
	}
	if c == nil {
		t.Fatalf("connection %s not persisted", id)
	}
	if c.ProviderSpecificData["orgId"] != "org-42" {
		t.Errorf("orgId not persisted: %+v", c.ProviderSpecificData)
	}
	if c.Provider != "kilocode" || c.Email != "o@b.c" {
		t.Errorf("trimmed fields wrong: %+v", c)
	}
	if c.AuthType != "import_token" || c.Status != "active" {
		t.Errorf("authType/status wrong: %+v", c)
	}
}

// 空/缺失 items 与超上限 → 400。
func TestConnectionImportBatchValidation(t *testing.T) {
	isolateAdminConnections(t)
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
	isolateAdminConnections(t)
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
	fp := ""
	for _, c := range connections.ListConnections() {
		if c.ProviderSpecificData["tokenFp"] != "" {
			fp = c.ProviderSpecificData["tokenFp"]
		}
	}
	if fp == "" {
		t.Fatal("expected a token fingerprint for the email-less item")
	}
	if strings.Contains(body, fp) {
		t.Errorf("response leaks token fingerprint %q: %s", fp, body)
	}
}

// installEmptyAdminPool installs an empty pool under PoolMu and restores the
// previous one when the test ends.
func installEmptyAdminPool(t *testing.T) {
	t.Helper()
	accounts.PoolMu.Lock()
	old := accounts.Pool
	accounts.Pool = &accounts.AccountPool{}
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool = old
		accounts.PoolMu.Unlock()
	})
}

// Authentication: once an admin password is set, connection routes refuse
// requests without a session.
func TestConnectionRoutesRequireAdminSession(t *testing.T) {
	installEmptyAdminPool(t)
	accounts.PoolMu.Lock()
	accounts.Pool.AdminPasswordHash = "hash-not-empty"
	accounts.Pool.AdminPasswordSalt = "salt"
	accounts.PoolMu.Unlock()
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	for _, path := range []string{"/admin/api/connections", "/admin/api/connections/test"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: status %d, want 401", path, rec.Code)
		}
	}
}

// Invalid JSON and missing targets keep their status codes and messages.
func TestConnectionAdapterErrorContracts(t *testing.T) {
	withAdminConnections(t, func() {
		cases := []struct {
			name    string
			call    func(http.ResponseWriter, *http.Request)
			body    string
			status  int
			message string
		}{
			{"login start invalid json", handleConnectionLoginStart, `{`, http.StatusBadRequest, "invalid_json"},
			{"import invalid json", handleConnectionImport, `{`, http.StatusBadRequest, "provider and accessToken required"},
			{"delete invalid json", handleConnectionDelete, `{`, http.StatusBadRequest, "invalid_json"},
			{"delete unknown id", handleConnectionDelete, `{"id":"conn_missing"}`, http.StatusNotFound, "connection not found"},
			{"test invalid json", handleConnectionTest, `{`, http.StatusBadRequest, "invalid_json"},
			{"test unknown id", handleConnectionTest, `{"id":"conn_missing"}`, http.StatusNotFound, "connection not found"},
		}
		for _, tc := range cases {
			rec := httptest.NewRecorder()
			tc.call(rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.body)))
			var env struct {
				Error string `json:"error"`
			}
			json.Unmarshal(rec.Body.Bytes(), &env)
			if rec.Code != tc.status || env.Error != tc.message {
				t.Errorf("%s: got %d %q, want %d %q", tc.name, rec.Code, env.Error, tc.status, tc.message)
			}
		}
	})
}

// Login status for an unknown session is 404 with the session message.
func TestConnectionLoginStatusNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	handleConnectionLoginStatus(rec, httptest.NewRequest(http.MethodGet, "/x?sessionId=nope", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "session not found") {
		t.Errorf("status %d body %s", rec.Code, rec.Body.String())
	}
}

// Upstream error: the probe reports the upstream status in a success envelope with
// ok=false, and the answer body is masked.
func TestConnectionProbeUpstreamErrorEnvelope(t *testing.T) {
	const secret = "admin-probe-secret-77"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("gateway echoed " + r.Header.Get("Authorization")))
	}))
	defer srv.Close()
	restore := connections.ProviderDeviceFlows
	connections.ProviderDeviceFlows = map[string]deviceflow.Spec{}
	for k, v := range restore {
		connections.ProviderDeviceFlows[k] = v
	}
	spec := connections.ProviderDeviceFlows["kilocode"]
	spec.ModelsTestURL = srv.URL
	connections.ProviderDeviceFlows["kilocode"] = spec
	defer func() { connections.ProviderDeviceFlows = restore }()

	withAdminConnections(t, func() {
		conn, err := connections.ImportConnectionToken("kilocode", secret, "probe@x.io", "")
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		handleConnectionTest(rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"id":"`+conn.ID+`"}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, secret) {
			t.Fatalf("probe response leaks the credential: %s", body)
		}
		if !strings.Contains(body, `"ok":false`) || !strings.Contains(body, `"status":502`) {
			t.Errorf("envelope = %s, want ok=false status=502", body)
		}
	})
}

// The list endpoint never returns a refresh token or a job token.
func TestConnectionListEndpointRedacts(t *testing.T) {
	withAdminConnections(t, func() {
		conn, err := connections.ImportConnectionToken("kilocode", "list-acc-9", "list@x.io", "")
		if err != nil {
			t.Fatal(err)
		}
		connections.UpsertConnection(&connections.Connection{
			Provider: "kilocode", Email: "list2@x.io", AccessToken: "list-acc-10",
			RefreshToken: "list-refresh-11", Status: "active",
			ProviderSpecificData: map[string]string{"exchangedToken": "list-job-12"},
		})
		_ = conn
		rec := httptest.NewRecorder()
		handleConnectionsList(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		body := rec.Body.String()
		for _, secret := range []string{"list-acc-9", "list-acc-10", "list-refresh-11", "list-job-12"} {
			if strings.Contains(body, secret) {
				t.Errorf("list leaks %q", secret)
			}
		}
	})
}
