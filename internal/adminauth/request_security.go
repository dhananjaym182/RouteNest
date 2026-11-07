package adminauth

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

var ErrRequestSecurity = errors.New("invalid administrator request security context")

type RequestSecurity struct {
	PeerIP         net.IP
	ClientIP       net.IP
	ExternalScheme string
	ExternalHost   string
	Secure         bool
	TrustedProxy   bool
}

type RequestSecurityOptions struct {
	CanonicalURL      *url.URL
	TrustedProxyCIDRs []*net.IPNet
	ProxyMode         bool
}

func ResolveRequestSecurity(r *http.Request, opts RequestSecurityOptions) (RequestSecurity, error) {
	var result RequestSecurity
	peer, err := parseRemoteIP(r.RemoteAddr)
	if err != nil {
		return result, fmt.Errorf("%w: peer address", ErrRequestSecurity)
	}
	result.PeerIP = peer
	result.ClientIP = peer
	result.TrustedProxy = ipInNetworks(peer, opts.TrustedProxyCIDRs)
	if opts.ProxyMode && strings.HasPrefix(r.URL.Path, "/admin/") && !result.TrustedProxy {
		return result, fmt.Errorf("%w: direct access rejected in proxy mode", ErrRequestSecurity)
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if result.TrustedProxy {
		forwarded := strings.TrimSpace(r.Header.Get("Forwarded"))
		hasXForwarded := hasAnyForwardedHeader(r.Header)
		if forwarded != "" && hasXForwarded {
			return result, fmt.Errorf("%w: conflicting forwarded header families", ErrRequestSecurity)
		}
		if forwarded != "" {
			var forwardedFor string
			scheme, host, forwardedFor, err = parseForwarded(forwarded)
			if err != nil {
				return result, err
			}
			if forwardedFor != "" {
				ip := parseForwardedIP(forwardedFor)
				if ip == nil {
					return result, fmt.Errorf("%w: invalid Forwarded for value", ErrRequestSecurity)
				}
				result.ClientIP = ip
			}
		} else if hasXForwarded {
			scheme, err = singleForwardedValue(r.Header.Values("X-Forwarded-Proto"), "scheme")
			if err != nil {
				return result, err
			}
			host, err = singleForwardedValue(r.Header.Values("X-Forwarded-Host"), "host")
			if err != nil {
				return result, err
			}
			result.ClientIP, err = forwardedClientIP(r.Header.Values("X-Forwarded-For"), opts.TrustedProxyCIDRs, peer)
			if err != nil {
				return result, err
			}
		}
	}

	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return result, fmt.Errorf("%w: invalid external scheme", ErrRequestSecurity)
	}
	host, err = NormalizeAuthority(host, scheme)
	if err != nil {
		return result, err
	}
	result.ExternalScheme = scheme
	result.ExternalHost = host
	result.Secure = scheme == "https"

	if opts.CanonicalURL != nil {
		canonicalScheme, canonicalHost, err := NormalizeCanonicalURL(opts.CanonicalURL)
		if err != nil {
			return result, err
		}
		if canonicalScheme != result.ExternalScheme || canonicalHost != result.ExternalHost {
			return result, fmt.Errorf("%w: request origin does not match canonical URL", ErrRequestSecurity)
		}
	}
	return result, nil
}

func NormalizeCanonicalURL(value *url.URL) (string, string, error) {
	if value == nil || value.User != nil || value.RawQuery != "" || value.Fragment != "" || (value.Path != "" && value.Path != "/") {
		return "", "", fmt.Errorf("%w: invalid canonical URL", ErrRequestSecurity)
	}
	scheme := strings.ToLower(value.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", fmt.Errorf("%w: invalid canonical URL scheme", ErrRequestSecurity)
	}
	host, err := NormalizeAuthority(value.Host, scheme)
	if err != nil {
		return "", "", err
	}
	return scheme, host, nil
}

func NormalizeAuthority(authority, scheme string) (string, error) {
	if authority == "" || strings.ContainsAny(authority, "/?#@\\") || strings.Contains(authority, "%") {
		return "", fmt.Errorf("%w: invalid external host", ErrRequestSecurity)
	}
	u, err := url.Parse("//" + authority)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: invalid external host", ErrRequestSecurity)
	}
	hostname := strings.ToLower(u.Hostname())
	if strings.HasSuffix(hostname, ".") || strings.ContainsAny(hostname, " \t\r\n") {
		return "", fmt.Errorf("%w: invalid external host", ErrRequestSecurity)
	}
	if net.ParseIP(hostname) == nil {
		for _, label := range strings.Split(hostname, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", fmt.Errorf("%w: invalid DNS host", ErrRequestSecurity)
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return "", fmt.Errorf("%w: invalid DNS host", ErrRequestSecurity)
				}
			}
		}
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		if strings.Contains(hostname, ":") {
			return "[" + hostname + "]", nil
		}
		return hostname, nil
	}
	return net.JoinHostPort(hostname, port), nil
}

func ValidateSameOrigin(r *http.Request, security RequestSecurity, mutation, allowLoopbackMissingOrigin bool) error {
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return fmt.Errorf("%w: cross-site request", ErrRequestSecurity)
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		if !mutation {
			return nil
		}
		if !allowLoopbackMissingOrigin || security.TrustedProxy || !security.PeerIP.IsLoopback() || !security.ClientIP.IsLoopback() || !authorityIsLoopback(security.ExternalHost) || (fetchSite != "" && fetchSite != "same-origin") {
			return fmt.Errorf("%w: mutation requires same origin", ErrRequestSecurity)
		}
		return nil
	}
	if len(origins) != 1 || strings.Contains(origins[0], ",") || strings.TrimSpace(origins[0]) == "null" {
		return fmt.Errorf("%w: invalid Origin", ErrRequestSecurity)
	}
	origin, err := url.Parse(strings.TrimSpace(origins[0]))
	if err != nil || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return fmt.Errorf("%w: invalid Origin", ErrRequestSecurity)
	}
	scheme := strings.ToLower(origin.Scheme)
	host, err := NormalizeAuthority(origin.Host, scheme)
	if err != nil || scheme != security.ExternalScheme || host != security.ExternalHost {
		return fmt.Errorf("%w: Origin mismatch", ErrRequestSecurity)
	}
	return nil
}

func authorityIsLoopback(authority string) bool {
	host := authority
	if parsed, _, err := net.SplitHostPort(authority); err == nil {
		host = parsed
	} else {
		host = strings.Trim(authority, "[]")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func parseRemoteIP(remote string) (net.IP, error) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.Trim(remote, "[]")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid remote address")
	}
	return ip, nil
}

func ipInNetworks(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func hasAnyForwardedHeader(header http.Header) bool {
	return len(header.Values("X-Forwarded-Proto")) > 0 || len(header.Values("X-Forwarded-Host")) > 0 || len(header.Values("X-Forwarded-For")) > 0
}

func singleForwardedValue(values []string, field string) (string, error) {
	if len(values) != 1 {
		return "", fmt.Errorf("%w: missing or repeated forwarded %s", ErrRequestSecurity, field)
	}
	parts := strings.Split(values[0], ",")
	if len(parts) != 1 || strings.TrimSpace(parts[0]) == "" {
		return "", fmt.Errorf("%w: multiple forwarded %s values", ErrRequestSecurity, field)
	}
	return strings.TrimSpace(parts[0]), nil
}

func forwardedClientIP(values []string, trusted []*net.IPNet, peer net.IP) (net.IP, error) {
	if len(values) == 0 {
		return peer, nil
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("%w: repeated X-Forwarded-For", ErrRequestSecurity)
	}
	parts := strings.Split(values[0], ",")
	chain := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		ip := parseForwardedIP(strings.TrimSpace(part))
		if ip == nil {
			return nil, fmt.Errorf("%w: invalid X-Forwarded-For", ErrRequestSecurity)
		}
		chain = append(chain, ip)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !ipInNetworks(chain[i], trusted) {
			return chain[i], nil
		}
	}
	if len(chain) > 0 {
		return chain[0], nil
	}
	return peer, nil
}

func parseForwarded(value string) (scheme, host, forwardedFor string, err error) {
	if strings.Contains(value, ",") {
		return "", "", "", fmt.Errorf("%w: multiple Forwarded elements", ErrRequestSecurity)
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(value, ";") {
		pair := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(pair) != 2 {
			return "", "", "", fmt.Errorf("%w: malformed Forwarded header", ErrRequestSecurity)
		}
		name := strings.ToLower(pair[0])
		if seen[name] {
			return "", "", "", fmt.Errorf("%w: repeated Forwarded parameter", ErrRequestSecurity)
		}
		seen[name] = true
		value := strings.Trim(strings.TrimSpace(pair[1]), "\"")
		switch name {
		case "proto":
			scheme = value
		case "host":
			host = value
		case "for":
			forwardedFor = value
		}
	}
	if scheme == "" || host == "" {
		return "", "", "", fmt.Errorf("%w: Forwarded proto and host required", ErrRequestSecurity)
	}
	return scheme, host, forwardedFor, nil
}

func parseForwardedIP(value string) net.IP {
	value = strings.Trim(value, "\"")
	if strings.HasPrefix(value, "[") {
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		} else {
			value = strings.Trim(value, "[]")
		}
	} else if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return net.ParseIP(value)
}
