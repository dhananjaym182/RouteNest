package admin

import (
	"fmt"
	"net/http"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"

	"routenest/internal/adminauth"
)

type csrfMode uint8

const (
	csrfUnspecified csrfMode = iota
	csrfSafe
	csrfSession
	csrfLocalEntry
	csrfSSOStart
	csrfOIDCCallback
	csrfSAMLACS
)

type adminRouteDefinition struct {
	Method      string
	Path        string
	Permissions []adminauth.Permission
	Public      bool
	CSRF        csrfMode
	Handler     http.Handler
}

type RoutePolicy struct {
	Method      string                 `json:"method"`
	Path        string                 `json:"path"`
	Permissions []adminauth.Permission `json:"permissions,omitempty"`
	Public      bool                   `json:"public"`
	CSRF        string                 `json:"csrf"`
}

type adminRouteRegistry struct {
	mux    *http.ServeMux
	byKey  map[string]adminRouteDefinition
	byPath map[string]map[string]struct{}
}

func newAdminRouteRegistry() *adminRouteRegistry {
	return &adminRouteRegistry{
		mux:    http.NewServeMux(),
		byKey:  make(map[string]adminRouteDefinition),
		byPath: make(map[string]map[string]struct{}),
	}
}

func routeKey(method, path string) string {
	return method + " " + path
}

func (r *adminRouteRegistry) Handle(def adminRouteDefinition) error {
	if err := validateAdminRoute(def); err != nil {
		return err
	}
	key := routeKey(def.Method, def.Path)
	if _, exists := r.byKey[key]; exists {
		return fmt.Errorf("duplicate admin route %s", key)
	}
	def.Permissions = append([]adminauth.Permission(nil), def.Permissions...)
	r.byKey[key] = def
	if r.byPath[def.Path] == nil {
		r.byPath[def.Path] = make(map[string]struct{})
	}
	r.byPath[def.Path][def.Method] = struct{}{}
	r.mux.Handle(def.Method+" "+def.Path, def.Handler)
	return nil
}

func (r *adminRouteRegistry) MustHandle(def adminRouteDefinition) {
	if err := r.Handle(def); err != nil {
		panic("admin route policy: " + err.Error())
	}
}

func (r *adminRouteRegistry) Policy(method, path string) (adminRouteDefinition, bool) {
	def, ok := r.byKey[routeKey(method, path)]
	if !ok && method == http.MethodHead {
		def, ok = r.byKey[routeKey(http.MethodGet, path)]
	}
	if !ok {
		return adminRouteDefinition{}, false
	}
	def.Permissions = append([]adminauth.Permission(nil), def.Permissions...)
	return def, true
}

func (r *adminRouteRegistry) AllowedMethods(path string) []string {
	methods := r.byPath[path]
	result := make([]string, 0, len(methods))
	for method := range methods {
		result = append(result, method)
	}
	sort.Strings(result)
	return result
}

func (r *adminRouteRegistry) Definitions() []adminRouteDefinition {
	definitions := make([]adminRouteDefinition, 0, len(r.byKey))
	for _, def := range r.byKey {
		def.Permissions = append([]adminauth.Permission(nil), def.Permissions...)
		definitions = append(definitions, def)
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Path == definitions[j].Path {
			return definitions[i].Method < definitions[j].Method
		}
		return definitions[i].Path < definitions[j].Path
	})
	return definitions
}

func (r *adminRouteRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func AdminRoutePolicies() []RoutePolicy {
	registry := newAdminRouteRegistry()
	registerSessionRoutes(registry)
	registerAccountsRoutes(registry)
	registerKeysRoutes(registry)
	registerModelsRoutes(registry)
	registerUpstreamsRoutes(registry)
	registerConfigRoutes(registry)
	registerProvidersRoutes(registry)
	registerProviderSystemRoutes(registry)
	registerConnectionsRoutes(registry)
	registerCombosRoutes(registry)
	registerObservabilityRoutes(registry)
	registerMiscRoutes(registry)
	definitions := registry.Definitions()
	policies := make([]RoutePolicy, 0, len(definitions))
	for _, definition := range definitions {
		policies = append(policies, RoutePolicy{
			Method:      definition.Method,
			Path:        definition.Path,
			Permissions: append([]adminauth.Permission(nil), definition.Permissions...),
			Public:      definition.Public,
			CSRF:        definition.CSRF.String(),
		})
	}
	return policies
}

func (mode csrfMode) String() string {
	switch mode {
	case csrfSafe:
		return "safe"
	case csrfSession:
		return "session"
	case csrfLocalEntry:
		return "local_entry"
	case csrfSSOStart:
		return "sso_start"
	case csrfOIDCCallback:
		return "oidc_callback"
	case csrfSAMLACS:
		return "saml_acs"
	default:
		return "unspecified"
	}
}

func validateAdminRoute(def adminRouteDefinition) error {
	if def.Handler == nil {
		return fmt.Errorf("%s %s has no handler", def.Method, def.Path)
	}
	if !validAdminMethod(def.Method) {
		return fmt.Errorf("%s %s has invalid method", def.Method, def.Path)
	}
	if err := validateAdminPath(def.Path); err != nil {
		return fmt.Errorf("%s %s: %w", def.Method, def.Path, err)
	}
	if def.Public && len(def.Permissions) != 0 {
		return fmt.Errorf("%s %s is public and permission-protected", def.Method, def.Path)
	}
	if !def.Public && len(def.Permissions) == 0 {
		return fmt.Errorf("%s %s has no permission", def.Method, def.Path)
	}
	seen := make(map[adminauth.Permission]struct{}, len(def.Permissions))
	for _, permission := range def.Permissions {
		if !adminauth.ValidPermission(permission) {
			return fmt.Errorf("%s %s has invalid permission %q", def.Method, def.Path, permission)
		}
		if _, duplicate := seen[permission]; duplicate {
			return fmt.Errorf("%s %s repeats permission %q", def.Method, def.Path, permission)
		}
		seen[permission] = struct{}{}
	}
	return validateCSRFPolicy(def)
}

func validAdminMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validateAdminPath(path string) error {
	if !strings.HasPrefix(path, "/admin/api/") || pathpkg.Clean(path) != path || strings.Contains(path, "\\") {
		return fmt.Errorf("invalid admin API path")
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.Path != path || u.RawQuery != "" || u.Fragment != "" || u.Host != "" {
		return fmt.Errorf("invalid admin API path")
	}
	return nil
}

func validateCSRFPolicy(def adminRouteDefinition) error {
	safeMethod := def.Method == http.MethodGet || def.Method == http.MethodHead
	if safeMethod {
		switch def.CSRF {
		case csrfSafe:
			return nil
		case csrfOIDCCallback:
			if !def.Public || def.Method != http.MethodGet || def.Path != "/admin/api/auth/oidc/callback" {
				return fmt.Errorf("%s %s invalid OIDC callback exemption", def.Method, def.Path)
			}
			return nil
		default:
			return fmt.Errorf("%s %s safe method must declare safe CSRF policy", def.Method, def.Path)
		}
	}

	switch def.CSRF {
	case csrfSession:
		if def.Public {
			return fmt.Errorf("%s %s public mutation cannot use session CSRF", def.Method, def.Path)
		}
	case csrfLocalEntry:
		if !def.Public || def.Method != http.MethodPost || (def.Path != "/admin/api/login" && def.Path != "/admin/api/auth/bootstrap" && def.Path != "/admin/api/auth/legacy/rotate") {
			return fmt.Errorf("%s %s invalid local-entry exemption", def.Method, def.Path)
		}
	case csrfSSOStart:
		if !def.Public || def.Method != http.MethodPost || (def.Path != "/admin/api/auth/oidc/start" && def.Path != "/admin/api/auth/saml/start") {
			return fmt.Errorf("%s %s invalid SSO-start exemption", def.Method, def.Path)
		}
	case csrfSAMLACS:
		if !def.Public || def.Method != http.MethodPost || def.Path != "/admin/api/auth/saml/acs" {
			return fmt.Errorf("%s %s invalid SAML ACS exemption", def.Method, def.Path)
		}
	default:
		return fmt.Errorf("%s %s mutation has no CSRF policy", def.Method, def.Path)
	}
	return nil
}
