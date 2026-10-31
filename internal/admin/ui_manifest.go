package admin

// adminUIJSAssets lists the admin script files in assembly order. The files share
// one global scope, so order matters: a later file may rely on a declaration in an
// earlier one. A new JS file must be added here; the manifest test rejects files
// that are present but not listed, and listed names that do not exist.
var adminUIJSAssets = []string{
	"ui/js/01_core.js",
	"ui/js/02_accounts.js",
	"ui/js/03_settings.js",
	"ui/js/04_providers.js",
	"ui/js/05_combos.js",
	"ui/js/06_models.js",
	"ui/js/07_logs.js",
	"ui/js/08_dashboard.js",
	"ui/js/09_init.js",
	"ui/js/10_quota.js",
}
