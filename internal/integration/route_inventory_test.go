package integration

// Route inventory characterization (plan Phase 0, item 1).
//
// Two layers:
//  1. Static inventory: every mux.HandleFunc("<literal>", ...) registration in
//     the public server (internal/proxycore/server.go, proxy.go) and admin registrar
//     (internal/admin/admin.go), with the wrapper chain and handler name.
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
	"../admin/admin.go",
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

// wrapperChain unwraps nested calls: auth(CorsHandler(h)) -> "auth>CorsHandler", handler "h".
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
			chain, handler := wrapperChain(call.Args[1])
			wrap := "none"
			if len(chain) > 0 {
				wrap = strings.Join(chain, ">")
			}
			lines = append(lines, fmt.Sprintf("%s\t%s\twrap=%s\thandler=%s", path, filepath.Base(src), wrap, handler))
			return true
		})
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
}

func probeLine(t *testing.T, mux http.Handler, method, path, body string) string {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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
	admin.RegisterRoutes(mux)

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
