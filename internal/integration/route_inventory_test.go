package integration

// Route inventory characterization (plan Phase 0, item 1).
//
// Two layers:
//  1. Static inventory: public mux registrations plus the fail-closed admin
//     method/path policy registry exposed by admin.AdminRoutePolicies.
//     Public routes are built inline inside proxycore.Start, which binds a port
//     and starts background loops, so they cannot be probed from a test.
//  2. Live probe: a fixed allowlist of read-only admin GET routes and one
//     invalid-body login POST, served through the mux built by
//     admin.RegisterRoutes. The probe records status, Content-Type and envelope
//     keys. Mutating routes are deliberately not sent requests.
//
// Goldens: testdata/route_inventory.golden and testdata/route_probe.golden.
// Regenerate with -update-route-golden after an intended change.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"routenest/internal/admin"
)

var updateRouteGolden = flag.Bool("update-route-golden", false, "rewrite route inventory and probe goldens")

// registrationSources are the files that call mux.HandleFunc with literal paths.
var registrationSources = []string{
	"../proxycore/proxy.go",
	"../proxycore/server.go",
}

// routeMethods is the reviewed method contract for every registered path. The
// inventory test fails when a route is missing here or when this table names a
// path the mux no longer registers, so adding or removing a route cannot leave
// the documented contract stale. "GET|POST" means the handler deliberately
// serves both (account export: download link and form post).
var routeMethods = map[string]string{
	"/admin/":                              "GET",
	"/admin/api/account-health":            "GET",
	"/admin/api/accounts":                  "GET",
	"/admin/api/accounts/add":              "POST",
	"/admin/api/accounts/delete":           "POST",
	"/admin/api/accounts/delete-all":       "POST",
	"/admin/api/accounts/export":           "POST",
	"/admin/api/accounts/refresh-all":      "POST",
	"/admin/api/accounts/reset":            "POST",
	"/admin/api/accounts/test":             "POST",
	"/admin/api/auth/audit":                "GET",
	"/admin/api/auth/bootstrap":            "POST",
	"/admin/api/auth/legacy/rotate":        "POST",
	"/admin/api/auth/oidc/callback":        "GET",
	"/admin/api/auth/oidc/start":           "POST",
	"/admin/api/auth/password":             "POST",
	"/admin/api/auth/saml/acs":             "POST",
	"/admin/api/auth/saml/metadata":        "GET",
	"/admin/api/auth/saml/start":           "POST",
	"/admin/api/auth/session":              "GET",
	"/admin/api/auth/sessions":             "GET",
	"/admin/api/auth/sessions/revoke":      "POST",
	"/admin/api/auth/sso/providers":        "GET|PUT",
	"/admin/api/auth/sso/providers/delete": "POST",
	"/admin/api/auth/status":               "GET",
	"/admin/api/auth/users":                "GET|POST",
	"/admin/api/auth/users/delete":         "POST",
	"/admin/api/auth/users/password":       "POST",
	"/admin/api/auth/users/update":         "POST",
	"/admin/api/batch-import":              "POST",
	"/admin/api/cline-proxy/config":        "GET",
	"/admin/api/cline-proxy/config/update": "POST",
	"/admin/api/combos":                    "GET",
	"/admin/api/combos/clone":              "POST",
	"/admin/api/combos/delete":             "POST",
	"/admin/api/combos/save":               "POST",
	"/admin/api/combos/test":               "POST",
	"/admin/api/combos/toggle":             "POST",
	"/admin/api/config":                    "GET",
	"/admin/api/config/update":             "POST",
	"/admin/api/connections":               "GET",
	"/admin/api/connections/delete":        "POST",
	"/admin/api/connections/import":        "POST",
	"/admin/api/connections/import-batch":  "POST",
	"/admin/api/connections/login/start":   "POST",
	"/admin/api/connections/login/status":  "GET",
	"/admin/api/connections/test":          "POST",
	"/admin/api/dashboard/overview":        "GET",
	"/admin/api/keys":                      "GET",
	"/admin/api/keys/delete":               "POST",
	"/admin/api/keys/generate":             "POST",
	"/admin/api/login":                     "POST",
	"/admin/api/listener-operation":        "GET",
	"/admin/api/logout":                    "POST",
	"/admin/api/models":                    "GET",
	"/admin/api/models/add":                "POST",
	"/admin/api/models/capabilities":       "POST",
	"/admin/api/models/context":            "POST",
	"/admin/api/models/cost":               "POST",
	"/admin/api/models/delete":             "POST",
	"/admin/api/models/import/apply":       "POST",
	"/admin/api/models/import/batch":       "GET",
	"/admin/api/models/import/discard":     "POST",
	"/admin/api/models/import/pending":     "GET",
	"/admin/api/models/import/preview":     "POST",
	"/admin/api/models/sync":               "POST",
	"/admin/api/models/test":               "POST",
	"/admin/api/models/toggle":             "POST",
	"/admin/api/oauth/start":               "POST",
	"/admin/api/oauth/status":              "GET",
	"/admin/api/open-external":             "POST",
	"/admin/api/opencode/config":           "GET",
	"/admin/api/opencode/config/update":    "POST",
	"/admin/api/opencode/models/sync":      "POST",
	"/admin/api/providers":                 "GET",
	"/admin/api/providers/cooldown-reset":  "POST",
	"/admin/api/providers/delete":          "POST",
	"/admin/api/providers/models":          "POST",
	"/admin/api/providers/presets":         "GET",
	"/admin/api/providers/recheck":         "POST",
	"/admin/api/providers/save":            "POST",
	"/admin/api/providers/sync-status":     "GET",
	"/admin/api/providers/test":            "POST",
	"/admin/api/provider-instances":        "GET",
	"/admin/api/credentials":               "GET",
	"/admin/api/model-offerings":           "GET",
	"/admin/api/provider-state":            "GET",
	"/admin/api/egress-profiles":           "GET",
	"/admin/api/request-logs":              "GET",
	"/admin/api/request-logs/clear":        "POST",
	"/admin/api/sso/import":                "POST",
	"/admin/api/stats":                     "GET",
	"/admin/api/version-check":             "GET",
	"/chat/completions":                    "POST",
	"/health":                              "GET",
	"/messages":                            "POST",
	"/messages/count_tokens":               "POST",
	"/models":                              "GET",
	"/responses":                           "POST",
	"/v1/chat/completions":                 "POST",
	"/v1/health":                           "GET",
	"/v1/messages":                         "POST",
	"/v1/messages/count_tokens":            "POST",
	"/v1/models":                           "GET",
	"/v1/responses":                        "POST",
}

// exprName renders an identifier or selector chain such as "providers.HandleList".
func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprName(t.X) + "." + t.Sel.Name
	case *ast.CallExpr:
		return exprName(t.Fun) + "(...)"
	case *ast.FuncLit:
		return "func-literal"
	}
	return "?"
}

// wrapperChain unwraps nested calls: auth(CORS(h)) -> "auth>CORS", handler "h".
func wrapperChain(e ast.Expr) (chain []string, handler string) {
	for {
		call, ok := e.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return chain, exprName(e)
		}
		chain = append(chain, exprName(call.Fun))
		e = call.Args[0]
	}
}

func staticRouteInventory(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	var lines []string
	seen := map[string]string{}
	for _, src := range registrationSources {
		file, err := parser.ParseFile(fset, src, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", src, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" || len(call.Args) != 2 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			path, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if prev, dup := seen[path]; dup {
				t.Errorf("route %s is registered twice (in %s and %s)", path, prev, filepath.Base(src))
				return true
			}
			seen[path] = filepath.Base(src)
			method, ok := routeMethods[path]
			if !ok {
				t.Errorf("route %s has no entry in routeMethods", path)
				method = "UNKNOWN"
			}
			chain, handler := wrapperChain(call.Args[1])
			wrap := "none"
			if len(chain) > 0 {
				wrap = strings.Join(chain, ">")
			}
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s\twrap=%s\thandler=%s", path, method, filepath.Base(src), wrap, handler))
			return true
		})
	}
	seen["/admin/"] = "admin.go"
	lines = append(lines, "/admin/\tGET\tadmin.go\twrap=adminSecurity.staticHandler\thandler=adminStaticHandler")
	adminMethods := map[string][]string{}
	for _, policy := range admin.AdminRoutePolicies() {
		adminMethods[policy.Path] = append(adminMethods[policy.Path], policy.Method)
		permissions := "public"
		if len(policy.Permissions) > 0 {
			values := make([]string, len(policy.Permissions))
			for i, permission := range policy.Permissions {
				values[i] = string(permission)
			}
			permissions = strings.Join(values, "+")
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\troutes.go\twrap=security[%s,csrf=%s]\thandler=registry", policy.Path, policy.Method, permissions, policy.CSRF))
	}
	for path, methods := range adminMethods {
		sort.Strings(methods)
		actual := strings.Join(methods, "|")
		expected, ok := routeMethods[path]
		if !ok {
			t.Errorf("admin route %s has no entry in routeMethods", path)
		} else if actual != expected {
			t.Errorf("admin route %s methods = %s, want %s", path, actual, expected)
		}
		seen[path] = "routes.go"
	}
	for path := range routeMethods {
		if _, ok := seen[path]; !ok {
			t.Errorf("routeMethods lists %s, which no registration source registers", path)
		}
	}
	sort.Strings(lines)
	return lines
}

func TestRouteInventoryStatic(t *testing.T) {
	lines := staticRouteInventory(t)
	if len(lines) < 80 {
		t.Fatalf("found %d registrations, expected at least 80 (public + admin)", len(lines))
	}
	got := []byte(strings.Join(lines, "\n") + "\n")
	compareGolden(t, filepath.Join("testdata", "route_inventory.golden"), got, *updateRouteGolden)
}

// probeRoutes are read-only GET endpoints. None of them is expected to write
// state or contact an upstream when the data directory is empty.
var probeRoutes = []string{
	"/admin/",
	"/admin/api/auth/status",
	"/admin/api/auth/session",
	"/admin/api/auth/sessions",
	"/admin/api/auth/users",
	"/admin/api/auth/audit",
	"/admin/api/auth/sso/providers",
	"/admin/api/auth/oidc/callback",
	"/admin/api/auth/saml/metadata",
	"/admin/api/listener-operation",
	"/admin/api/providers/presets",
	"/admin/api/providers",
	"/admin/api/connections",
	"/admin/api/models",
	"/admin/api/config",
	"/admin/api/keys",
	"/admin/api/accounts",
	"/admin/api/combos",
	"/admin/api/request-logs",
	"/admin/api/account-health",
	"/admin/api/providers/sync-status",
	"/admin/api/opencode/config",
	"/admin/api/cline-proxy/config",
	"/admin/api/provider-instances",
	"/admin/api/credentials",
	"/admin/api/model-offerings",
	"/admin/api/provider-state",
	"/admin/api/egress-profiles",
}

// unprobedGETRoutes names the GET-capable routes the probe deliberately skips,
// with the reason. The coverage test below requires every GET route to be either
// probed or listed here, so a new endpoint cannot be added without deciding
// whether it is safe to characterize.
var unprobedGETRoutes = map[string]string{
	"/admin/api/connections/login/status": "requires a live login session id",
	"/admin/api/dashboard/overview":       "covered by internal/admin dashboard tests; reads mutable counters",
	"/admin/api/models/import/batch":      "requires a staged import id",
	"/admin/api/models/import/pending":    "returns staged import state",
	"/admin/api/oauth/status":             "requires a device-flow session id, starts no safe read",
	"/admin/api/stats":                    "covered by internal/admin tests; reads mutable counters",
	"/admin/api/version-check":            "may contact the release host when the cache is cold",
}

// Every GET route is either probed for its envelope or explicitly exempted.
func TestRouteProbeCoversEveryGETRoute(t *testing.T) {
	probed := map[string]bool{}
	for _, p := range probeRoutes {
		probed[p] = true
	}
	for path, method := range routeMethods {
		if !strings.Contains(method, "GET") || !strings.HasPrefix(path, "/admin/") {
			continue
		}
		if probed[path] {
			continue
		}
		if _, ok := unprobedGETRoutes[path]; !ok {
			t.Errorf("GET route %s is neither probed nor listed in unprobedGETRoutes", path)
		}
	}
	for path := range unprobedGETRoutes {
		if probed[path] {
			t.Errorf("unprobedGETRoutes lists %s, but the probe already covers it", path)
		}
	}
}

func probeLine(t *testing.T, mux http.Handler, method, path, body string) string {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:3457"+path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:41000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", "http://127.0.0.1:3457")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	shape := "non-json"
	if strings.Contains(ct, "application/json") {
		shape = jsonEnvelopeShape(rec.Body.Bytes())
	}
	return fmt.Sprintf("%s %s -> %d ct=%s shape=%s", method, path, rec.Code, ct, shape)
}

// jsonEnvelopeShape lists top-level keys, plus sorted keys of an object "data".
// Values are omitted so time-dependent counters do not affect the golden.
func jsonEnvelopeShape(body []byte) string {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return "invalid-json"
	}
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "{" + strings.Join(keys, ",") + "}"
	if data, ok := root["data"].(map[string]any); ok {
		dk := make([]string, 0, len(data))
		for k := range data {
			dk = append(dk, k)
		}
		sort.Strings(dk)
		out += " data{" + strings.Join(dk, ",") + "}"
	}
	return out
}

func TestRouteProbeAdminAPI(t *testing.T) {
	mux := http.NewServeMux()
	integrationAdminRegistrar(t, "http://127.0.0.1:3457")(mux)

	var lines []string
	for _, p := range probeRoutes {
		lines = append(lines, probeLine(t, mux, http.MethodGet, p, ""))
	}
	// Unauthenticated invalid-body login: exercises the public login route envelope
	// without setting or checking a password.
	lines = append(lines, probeLine(t, mux, http.MethodPost, "/admin/api/login", "not-json"))

	got := []byte(strings.Join(lines, "\n") + "\n")
	compareGolden(t, filepath.Join("testdata", "route_probe.golden"), got, *updateRouteGolden)
}

func compareGolden(t *testing.T, path string, got []byte, update bool) {
	t.Helper()
	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update-route-golden): %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs; review and rerun with -update-route-golden if intended\n--- got\n%s", path, got)
	}
}

// publicProbes are requests that never reach an upstream: health, model
// listing, CORS preflight, and invalid-JSON bodies rejected during parsing.
var publicProbes = []struct{ method, path, body string }{
	{http.MethodGet, "/health", ""},
	{http.MethodGet, "/v1/health", ""},
	{http.MethodGet, "/v1/models", ""},
	{http.MethodGet, "/models", ""},
	{http.MethodOptions, "/v1/chat/completions", ""},
	{http.MethodPost, "/v1/chat/completions", "not-json"},
	{http.MethodPost, "/chat/completions", "not-json"},
	{http.MethodPost, "/v1/messages", "not-json"},
	{http.MethodPost, "/messages", "not-json"},
	{http.MethodPost, "/v1/messages/count_tokens", "not-json"},
	{http.MethodPost, "/messages/count_tokens", "not-json"},
	{http.MethodPost, "/v1/responses", "not-json"},
	{http.MethodPost, "/responses", "not-json"},
}

// TestRouteProbePublicAPI runs the real production mux (protocolTestServer), so
// the public route aliases and their envelopes are recorded from the live handler chain.
func TestRouteProbePublicAPI(t *testing.T) {
	installPool(t, nil)
	baseURL := protocolTestServer(t)
	client := &http.Client{Timeout: 5 * time.Second}

	var lines []string
	for _, p := range publicProbes {
		req, err := http.NewRequest(p.method, baseURL+p.path, strings.NewReader(p.body))
		if err != nil {
			t.Fatal(err)
		}
		if p.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", p.method, p.path, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		ct := resp.Header.Get("Content-Type")
		shape := "non-json"
		if strings.Contains(ct, "application/json") {
			shape = jsonEnvelopeShape(raw)
		}
		lines = append(lines, fmt.Sprintf("%s %s -> %d ct=%s shape=%s", p.method, p.path, resp.StatusCode, ct, shape))
	}
	compareGolden(t, filepath.Join("testdata", "route_probe_public.golden"), []byte(strings.Join(lines, "\n")+"\n"), *updateRouteGolden)
}
