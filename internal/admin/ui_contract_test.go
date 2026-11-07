package admin

// Admin UI contract gates (plan Phase 0 item 5 and Phase 9 verification).
//
// The console is assembled from fragments that share one global script scope.
// These tests pin the parts that break silently:
//   - the page-panel, overlay and navigation IDs the scripts look up;
//   - every `_('id')` reference resolves to markup or to JS-generated markup;
//   - every literal switchTab/hash target has a matching tab panel;
//   - no JS asset exceeds the 1,000-line limit stated in admin_html.go;
//   - the initialization asset is last in the manifest.

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// requiredAdminIDs are the stable identifiers other code and the documented
// workflows depend on. Renaming one is a UI contract break, not a refactor.
var requiredAdminIDs = []string{
	"tab-dashboard", "tab-accounts", "tab-logs", "tab-security",
	"tab-routing", "tab-combos", "tab-upstreams", "tab-providers",
	"tab-quickstart", "tab-quota", "tab-about", "tab-global",
	"tab-credentials", "tab-models", "tab-network", "tab-health",
	"globalProviderFilter", "globalInstances", "globalCredentials",
	"globalOfferings", "globalState", "globalEgress",
	"globalCredentialProviderFilter", "globalModelProviderFilter",
	"globalHealthProviderFilter", "globalNetworkContext",
	"globalOfferingsToggle",
	"connectProviderBtn", "providerSetup", "providerSetupOverlay", "providerSetupContent", "providerAuthSetup",

	"toastStack", "loginOverlay", "loginPassword", "loginError",
	"langZh", "langEn", "themeSwitch", "refreshAllBtn", "saveDiscardBar",
	"navEdgeToggle",
}

var (
	markupIDRe   = regexp.MustCompile(`\bid="([A-Za-z0-9_-]+)"`)
	jsLookupIDRe = regexp.MustCompile(`_\(['"]([A-Za-z0-9_-]+)['"]\)`)
	switchTabRe  = regexp.MustCompile(`switchTab\(['"]([A-Za-z0-9_-]+)['"]\)`)
)

func adminJSAssets(t *testing.T) map[string]string {
	t.Helper()
	out := make(map[string]string, len(adminUIJSAssets))
	for _, name := range adminUIJSAssets {
		b, err := adminUIFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(b)
	}
	return out
}

func adminUIAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := adminUIFS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestAdminUIMarkupHasRequiredIDs(t *testing.T) {
	have := map[string]bool{}
	for _, m := range markupIDRe.FindAllStringSubmatch(adminHTML, -1) {
		have[m[1]] = true
	}
	var missing []string
	for _, id := range requiredAdminIDs {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("required admin DOM ids are missing: %v", missing)
	}
}

// Every `_('id')` lookup must resolve to an element in the assembled markup or to
// an element a script renders. The scripts tolerate a missing element in places,
// so a renamed ID otherwise fails only at click time.
func TestAdminJSIDLookupsResolve(t *testing.T) {
	known := map[string]bool{}
	for _, m := range markupIDRe.FindAllStringSubmatch(adminHTML, -1) {
		known[m[1]] = true
	}
	assets := adminJSAssets(t)
	for _, src := range assets {
		for _, m := range markupIDRe.FindAllStringSubmatch(src, -1) {
			known[m[1]] = true
		}
	}
	missing := map[string][]string{}
	for name, src := range assets {
		for _, m := range jsLookupIDRe.FindAllStringSubmatch(src, -1) {
			if !known[m[1]] {
				missing[m[1]] = append(missing[m[1]], name)
			}
		}
	}
	if len(missing) > 0 {
		var names []string
		for id, files := range missing {
			names = append(names, id+" ("+strings.Join(files, ", ")+")")
		}
		sort.Strings(names)
		t.Fatalf("script lookups with no element in markup or JS-generated markup: %v", names)
	}
}

// Literal tab targets (inline handlers and hash routing) must have a panel.
// "import" is the documented back-compat alias for the accounts tab.
func TestAdminSwitchTabTargetsExist(t *testing.T) {
	known := map[string]bool{}
	for _, m := range markupIDRe.FindAllStringSubmatch(adminHTML, -1) {
		known[m[1]] = true
	}
	targets := map[string]bool{}
	for _, m := range switchTabRe.FindAllStringSubmatch(adminHTML, -1) {
		targets[m[1]] = true
	}
	for _, src := range adminJSAssets(t) {
		for _, m := range switchTabRe.FindAllStringSubmatch(src, -1) {
			targets[m[1]] = true
		}
	}
	delete(targets, "import") // alias normalized to "accounts"
	var missing []string
	for name := range targets {
		if !known["tab-"+name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("switchTab targets with no tab panel: %v", missing)
	}
}

// admin_html.go states that each behaviour asset stays at or under 1,000 lines so
// one file keeps one page owner. The i18n dictionary is flat data, kept in its own
// asset and validated by tools/i18n_*.js, so it is exempt; every other asset is
// still bounded.
func TestAdminJSAssetsStayUnderLineLimit(t *testing.T) {
	const dictionary = "ui/js/01_i18n.js"
	for name, src := range adminJSAssets(t) {
		if name == dictionary {
			continue
		}
		if lines := strings.Count(src, "\n"); lines > 1000 {
			t.Errorf("%s has %d lines, want <= 1000", name, lines)
		}
	}
}

// The exempt dictionary asset must stay a pure dictionary: one const object and
// nothing executable after it, so no behaviour drifts back into the data file.
func TestAdminI18nAssetIsDictionaryOnly(t *testing.T) {
	src, ok := adminJSAssets(t)["ui/js/01_i18n.js"]
	if !ok {
		t.Fatal("ui/js/01_i18n.js is not in the manifest")
	}
	trimmed := strings.TrimSpace(src)
	if !strings.HasPrefix(trimmed, "// ===== i18n =====") {
		t.Errorf("dictionary asset must start with its banner")
	}
	if !strings.HasSuffix(trimmed, "};") {
		t.Errorf("dictionary asset must end with the closing object literal")
	}
	if strings.Contains(src, "function ") {
		t.Errorf("dictionary asset contains behaviour; move it to 01_core.js")
	}
}

// The initialization asset runs applyLang/applyTab and must be the last script,
// or later declarations are not yet evaluated when it runs.
func TestAdminUIManifestEndsWithInit(t *testing.T) {
	if len(adminUIJSAssets) == 0 {
		t.Fatal("manifest is empty")
	}
	last := adminUIJSAssets[len(adminUIJSAssets)-1]
	if last != "ui/js/16_init.js" {
		t.Fatalf("manifest ends with %s, want ui/js/16_init.js", last)
	}
}

func TestAdminUICanonicalNavigationLabels(t *testing.T) {
	tests := []struct {
		name  string
		asset string
		want  []string
	}{
		{
			name:  "sidebar",
			asset: "ui/html/shell.html",
			want: []string{
				`class="nav-section-label">工作区</div>`,
				`data-tab="providers"`,
				`class="nav-label">Provider 管理</span>`,
				`data-tab="credentials"`,
				`class="nav-label">账号与凭据</span>`,
				`data-tab="models"`,
				`class="nav-label">模型与授权</span>`,
				`data-tab="routing"`,
				`class="nav-label">路由策略</span>`,
				`data-tab="combos"`,
				`class="nav-label">路由组合</span>`,
				`data-tab="network"`,
				`class="nav-label">网络与出口</span>`,
				`data-tab="logs"`,
				`class="nav-label">请求日志</span>`,
				`data-tab="quota"`,
				`class="nav-label">用量与配额</span>`,
				`data-tab="health"`,
				`class="nav-label">Provider 健康</span>`,
			},
		},
		{
			name:  "credentials",
			asset: "ui/html/pages/accounts.html",
			want: []string{
				`id="tab-credentials"`,
				`class="large-title">账号与凭据</div>`,
				`data-sub="inventory">API 凭据</button>`,
				`data-sub="conns">OAuth 已连接账号</button>`,
				`id="connectionsList"`,
				`id="accountTableBody"`,
			},
		},
		{
			name:  "routing",
			asset: "ui/html/pages/routing.html",
			want: []string{
				`class="large-title">路由策略</div>`,
				`data-sub="config">路由策略</div>`,
				`data-sub="tuning">运行调优</div>`,
				`data-sub="models" title="打开模型页">模型目录 ↗</div>`,
			},
		},
		{
			name:  "network",
			asset: "ui/html/pages/upstreams.html",
			want: []string{
				`id="tab-network"`,
				`class="large-title">网络与出口</div>`,
				`id="clineProxies"`,
				`默认出口代理</div>`,
				`服务专属网络设置</span>`,
			},
		},
		{
			name:  "english translations",
			asset: "ui/js/01_i18n.js",
			want: []string{
				`'账号与凭据': 'Accounts & Credentials'`,
				`'Provider 管理': 'Providers'`,
				`'Cline 账号': 'Cline Accounts'`,
				`'Provider OAuth': 'Provider OAuth'`,
				`'路由策略': 'Routing Policy'`,
				`'运行调优': 'Runtime Tuning'`,
				`'模型目录': 'Model Catalog'`,
				`'网络与出口': 'Network & Egress'`,
				`'Provider 健康': 'Provider Health'`,
				`'出口代理池': 'Egress Proxies'`,
				`'OAuth 已连接账号': 'OAuth Connected Accounts'`,
				`'API 凭据': 'API Credentials'`,
				`'默认出口代理': 'Default outbound proxy'`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := adminUIAsset(t, tt.asset)
			for _, want := range tt.want {
				if !strings.Contains(src, want) {
					t.Errorf("%s missing canonical label %q", tt.asset, want)
				}
			}
		})
	}
}

func TestAdminUIUpstreamProxyOwnership(t *testing.T) {
	network := adminUIAsset(t, "ui/html/pages/upstreams.html")
	providers := adminUIAsset(t, "ui/html/pages/providers.html")
	for _, id := range []string{"clineProxyStrategy", "clineProxies", "clineProxySaveResult"} {
		if !strings.Contains(network, `id="`+id+`"`) {
			t.Errorf("global default egress missing %s", id)
		}
	}
	for _, id := range []string{"ocKey", "ocProxyStrategy", "ocProxies", "zenHeadersBulk", "headersTableBody"} {
		if strings.Contains(network, `id="`+id+`"`) {
			t.Errorf("provider-specific setting %s remains on Network", id)
		}
		if !strings.Contains(providers, `id="`+id+`"`) {
			t.Errorf("Providers missing native setting %s", id)
		}
	}
	accounts := adminUIAsset(t, "ui/html/pages/accounts.html")
	if strings.Contains(accounts, `data-sub="pool"`) || strings.Contains(accounts, `id="loginImportSection"`) {
		t.Error("credentials still owns a separate Cline setup destination")
	}
	for _, id := range []string{"loginImportSection", "connProviderGroups", "providerNativeOpenCode", "providerNativeCline"} {
		if !strings.Contains(providers, `id="`+id+`"`) {
			t.Errorf("Providers missing native setup %s", id)
		}
	}
}

func TestAdminUIDirectSubtabLinks(t *testing.T) {
	nav := adminUIAsset(t, "ui/js/02_nav.js")
	if !strings.Contains(nav, "function showPageSubTab(page, sub)") {
		t.Error("navigation helper showPageSubTab is missing")
	}
	routing := adminUIAsset(t, "ui/html/pages/routing.html")
	if !strings.Contains(routing, `showPageSubTab('upstreams', 'cline')`) {
		t.Error("routing policy does not link directly to upstream proxy settings")
	}
	upstreams := adminUIAsset(t, "ui/html/pages/upstreams.html")
	if !strings.Contains(upstreams, `showPageSubTab('routing', 'config')`) {
		t.Error("proxy settings do not link directly to routing policy")
	}
	quickstart := adminUIAsset(t, "ui/html/pages/quickstart.html")
	if !strings.Contains(quickstart, `showPageSubTab('accounts', 'pool')`) {
		t.Error("quick start does not link directly to Cline accounts")
	}
	if !strings.Contains(quickstart, `showPageSubTab('upstreams', 'zen')`) {
		t.Error("quick start does not link directly to OpenCode Zen")
	}
	comboEditor := adminUIAsset(t, "ui/html/dialogs/combo_editor.html")
	if !strings.Contains(comboEditor, `showPageSubTab('routing', 'config')`) {
		t.Error("combo editor does not link directly to routing policy")
	}
	dashboard := adminUIAsset(t, "ui/js/15_dashboard.js")
	if !strings.Contains(dashboard, `showPageSubTab('accounts', 'pool')`) {
		t.Error("dashboard account alerts do not link directly to Cline accounts")
	}
	if !strings.Contains(dashboard, `showPageSubTab('accounts', 'conns')`) {
		t.Error("dashboard login alerts do not link directly to Provider OAuth")
	}
}

func TestAdminUIProviderEditorDoesNotCloseApplicationShell(t *testing.T) {
	src := adminUIAsset(t, "ui/html/dialogs/provider_editor.html")
	if opens, closes := strings.Count(src, "<div"), strings.Count(src, "</div>"); opens != closes {
		t.Fatalf("provider editor fragment has %d opening divs and %d closing divs", opens, closes)
	}
}

func TestAdminUICanonicalAccountReferences(t *testing.T) {
	assets := []string{
		"ui/html/pages/accounts.html",
		"ui/html/pages/quickstart.html",
		"ui/html/dialogs/provider_editor.html",
		"ui/js/09_providers_pickers.js",
	}
	for _, name := range assets {
		src := adminUIAsset(t, name)
		for _, old := range []string{"账号与连接", "账号连接池"} {
			if strings.Contains(src, old) {
				t.Errorf("%s still uses ambiguous account term %q", name, old)
			}
		}
	}
}

func TestAdminUICanonicalOpenCodeLabels(t *testing.T) {
	tests := []struct {
		asset string
		want  string
	}{
		{"ui/html/pages/providers.html", "OpenCode Zen 今日用量"},
		{"ui/html/pages/models.html", "从 OpenCode Zen 同步模型"},
		{"ui/html/pages/providers.html", "OpenCode Zen 出口代理"},
		{"ui/html/pages/providers.html", "OpenCode Zen 请求头"},
		{"ui/js/11_upstreams.js", "t('OpenCode Zen 配置已保存')"},
		{"ui/js/05_settings.js", "t('OpenCode Zen 请求头已保存')"},
		{"ui/js/13_models.js", "zen: 'OpenCode Zen'"},
		{"ui/js/12_combos.js", `model-source-badge zen">OpenCode Zen</span>`},
	}
	for _, tt := range tests {
		if src := adminUIAsset(t, tt.asset); !strings.Contains(src, tt.want) {
			t.Errorf("%s missing canonical OpenCode label %q", tt.asset, tt.want)
		}
	}
}

// The global provider views project the additive admin endpoints into six
// canonical concepts. They remain read-only and explicit about unknown states.
func TestAdminGlobalProviderViewCoversCapabilityInventory(t *testing.T) {
	shell := adminUIAsset(t, "ui/html/shell.html")
	for _, tab := range []string{"providers", "credentials", "models", "routing", "network", "health"} {
		if !strings.Contains(shell, `class="nav-item`) || !strings.Contains(shell, `data-tab="`+tab+`"`) {
			t.Errorf("navigation has no canonical %s entry", tab)
		}
	}
	for _, legacy := range []string{"global", "accounts", "upstreams"} {
		if strings.Contains(shell, `data-tab="`+legacy+`"`) {
			t.Errorf("legacy %s remains a primary navigation item", legacy)
		}
	}
	for asset, id := range map[string]string{
		"ui/html/pages/providers.html": "globalInstances",
		"ui/html/pages/accounts.html":  "globalCredentials",
		"ui/html/pages/models.html":    "globalOfferings",
		"ui/html/pages/upstreams.html": "globalEgress",
		"ui/html/pages/health.html":    "globalState",
	} {
		if page := adminUIAsset(t, asset); !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("%s does not own %s", asset, id)
		}
	}
	js := adminUIAsset(t, "ui/js/18_global.js")
	for _, endpoint := range []string{
		"/provider-instances", "/credentials", "/model-offerings",
		"/provider-state", "/egress-profiles",
	} {
		if !strings.Contains(js, "'"+endpoint+"'") {
			t.Errorf("global view does not read %s", endpoint)
		}
	}
	for capability := range map[string]bool{
		"modelDiscovery": true, "freeModelDetection": true,
		"removedModelDetection": true, "reactiveQuota": true,
		"proactiveQuota": true, "quotaResetDetection": true,
		"credentialRotation": true, "egressPool": true,
		"contextCompaction": true,
	} {
		if !strings.Contains(js, capability) {
			t.Errorf("capability %q is not rendered", capability)
		}
	}
	for _, state := range []string{"'支持'", "'不支持'", "'未知'"} {
		if !strings.Contains(js, state) {
			t.Errorf("capability state %s is not rendered", state)
		}
	}
	for _, field := range []string{"observation.until", "observation.resetAt"} {
		if !strings.Contains(js, field) {
			t.Errorf("runtime state does not distinguish %s", field)
		}
	}
	for _, label := range []string{"上游确认重置", "回退冷却截止", "没有上游重置证据"} {
		if !strings.Contains(js, label) {
			t.Errorf("runtime state does not render %q", label)
		}
	}
}

func TestAdminUILegacyHashesAliasCanonicalConcepts(t *testing.T) {
	nav := adminUIAsset(t, "ui/js/02_nav.js")
	for legacy, canonical := range map[string]string{
		"global": "providers", "accounts": "credentials",
		"import": "credentials", "upstreams": "network",
	} {
		want := legacy + ": '" + canonical + "'"
		if !strings.Contains(nav, want) {
			t.Errorf("navigation missing alias %s", want)
		}
	}
	for _, id := range []string{"tab-global", "tab-accounts", "tab-upstreams"} {
		if !strings.Contains(adminHTML, `id="`+id+`"`) {
			t.Errorf("compatibility DOM id %s is missing", id)
		}
	}
}

func TestAdminUILegacyWorkflowsRemainReachable(t *testing.T) {
	routing := adminUIAsset(t, "ui/html/pages/routing.html")
	if !strings.Contains(routing, `switchTab('combos')`) {
		t.Error("routing does not link to model combinations")
	}
	shell := adminUIAsset(t, "ui/html/shell.html")
	for _, tab := range []string{"dashboard", "logs", "quota", "health", "quickstart", "security", "about"} {
		if !strings.Contains(shell, `data-tab="`+tab+`"`) {
			t.Errorf("sidebar workflow %s is no longer reachable", tab)
		}
	}
}

func TestAdminUIProviderDetailLinksFilteredGlobalViews(t *testing.T) {
	detail := adminUIAsset(t, "ui/js/07_providers_detail.js")
	for _, destination := range []string{"credentials", "models", "health", "network"} {
		if !strings.Contains(detail, "provGlobalViewButton('"+destination+"'") {
			t.Errorf("provider detail does not link to %s", destination)
		}
	}
	global := adminUIAsset(t, "ui/js/18_global.js")
	if !strings.Contains(global, "function openGlobalProviderView(tab, providerID)") ||
		!strings.Contains(global, "_globalFilter = String(providerID") {
		t.Error("provider detail destinations do not retain a provider filter")
	}
}
