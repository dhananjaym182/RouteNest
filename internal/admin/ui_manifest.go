package admin

// adminUIJSAssets lists the admin script files in assembly order. The files share
// one global scope, so order matters: a later file may rely on a declaration in an
// earlier one. A new JS file must be added here; the manifest test rejects files
// that are present but not listed, and listed names that do not exist.
var adminUIJSAssets = []string{
	"ui/js/01_i18n.js",
	"ui/js/01_core.js",
	"ui/js/02_nav.js",
	"ui/js/03_shell.js",
	"ui/js/04_accounts.js",
	"ui/js/05_settings.js",
	"ui/js/06_security_users.js",
	"ui/js/06_providers_custom.js",
	"ui/js/07_providers_detail.js",
	"ui/js/08_providers_detect.js",
	"ui/js/09_providers_pickers.js",
	"ui/js/10_providers_import.js",
	"ui/js/11_upstreams.js",
	"ui/js/11_connections.js",
	"ui/js/12_combo_catalog.js",
	"ui/js/12_combos.js",
	"ui/js/13_models.js",
	"ui/js/13_model_sync.js",
	"ui/js/13_model_cleanup.js",
	"ui/js/13_model_provider_actions.js",
	"ui/js/14_logs.js",
	"ui/js/15_dashboard.js",
	"ui/js/17_quota.js",
	"ui/js/18_global.js",
	"ui/js/19_provider_setup.js",
	"ui/js/20_routing_inspector.js",
	// 16_init runs applyLang/applyTab, so it must stay last.
	"ui/js/16_init.js",
}

// adminUIBodyParts lists the page body fragments in document order. Concatenated,
// they are the <body> markup. Tab panels are pages/*, overlays and drawers are
// dialogs/*. Plain HTML only; scripts live in adminUIJSAssets.
var adminUIBodyParts = []string{
	"ui/html/shell.html",
	"ui/html/pages/dashboard.html",
	"ui/html/pages/accounts.html",
	"ui/html/pages/logs.html",
	"ui/html/pages/security.html",
	"ui/html/pages/routing.html",
	"ui/html/pages/models.html",
	"ui/html/pages/combos.html",
	"ui/html/dialogs/combo_editor.html",
	"ui/html/dialogs/provider_detail.html",
	"ui/html/pages/upstreams.html",
	"ui/html/pages/providers.html",
	"ui/html/pages/global.html",
	"ui/html/pages/health.html",
	"ui/html/dialogs/provider_save_bar.html",
	"ui/html/dialogs/diff.html",
	"ui/html/dialogs/provider_editor.html",
	"ui/html/dialogs/provider_setup.html",
	"ui/html/pages/quickstart.html",
	"ui/html/pages/quota.html",
	"ui/html/pages/about.html",
	"ui/html/dialogs/toast.html",
	"ui/html/dialogs/login.html",
	"ui/html/dialogs/model_sync.html",
}

// adminUICSSLayers lists the stylesheet layers in cascade order. Later layers win
// over earlier ones, so the order is part of the styling. Each layer is a
// contiguous slice of the original stylesheet.
var adminUICSSLayers = []string{
	"ui/css/00_base.css",
	"ui/css/01_fonts.css",
	"ui/css/10_pages_dashboard.css",
	"ui/css/20_layout.css",
	"ui/css/30_components.css",
	"ui/css/40_pages_models.css",
	"ui/css/41_pages_combos.css",
	"ui/css/42_pages_providers_import.css",
	"ui/css/43_pages_provider_cards.css",
	"ui/css/44_pages_provider_detail.css",
	"ui/css/45_pages_provider_system.css",
	"ui/css/50_components_a11y.css",
	"ui/css/60_responsive.css",
	"ui/css/70_pages_overview_logs_health.css",
	"ui/css/80_waves.css",
	"ui/css/90_connection_import.css",
	"ui/css/95_control_plane.css",
	"ui/css/96_workflows.css",
	"ui/css/97_management_tables.css",
	"ui/css/98_routing_inspector.css",
	"ui/css/99_access_settings.css",
}
