//go:build desktop

package main

import (
	"embed"
	"io/fs"
	"net/http"
	"net/url"
	"runtime"
	"strings"
)

//go:embed desktop/setup/*
var embeddedDesktopSetup embed.FS

//go:embed desktop/assets/icon.png
var desktopAppIcon []byte

func desktopSetupAssets() fs.FS {
	assets, err := fs.Sub(embeddedDesktopSetup, "desktop/setup")
	if err != nil {
		panic(err)
	}
	return assets
}

func desktopAssetOrigin() string {
	if runtime.GOOS == "windows" {
		return "http://wails.localhost"
	}
	return "wails://wails"
}

func desktopBindingOrigins(string) string {
	return ""
}

func desktopAssetMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Cache-Control", "no-store")
		if !desktopAssetRequestAllowed(request) {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		switch request.URL.Path {
		case "/admin/":
		case "/", "/index.html", "/setup.js", "/setup.css":
			response.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; form-action 'none'")
		default:
			http.NotFound(response, request)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func desktopAssetRequestAllowed(request *http.Request) bool {
	expected, _ := url.Parse(desktopAssetOrigin())
	if request.URL == nil || request.Host != expected.Host || request.URL.User != nil || request.URL.Fragment != "" || request.URL.RawPath != "" || strings.Contains(request.URL.Path, "\\") {
		return false
	}
	if request.URL.Scheme != "" && request.URL.Scheme != expected.Scheme || request.URL.Host != "" && request.URL.Host != expected.Host {
		return false
	}
	if values := request.Header.Values("Host"); len(values) > 1 || len(values) == 1 && values[0] != expected.Host {
		return false
	}
	if origins := request.Header.Values("Origin"); len(origins) > 1 || len(origins) == 1 && origins[0] != desktopAssetOrigin() {
		return false
	}
	if referers := request.Header.Values("Referer"); len(referers) > 0 {
		if len(referers) != 1 {
			return false
		}
		referer, err := url.Parse(referers[0])
		if err != nil || referer.User != nil || referer.Scheme != expected.Scheme || referer.Host != expected.Host {
			return false
		}
	}
	site := request.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	switch request.Header.Get("Sec-Fetch-Dest") {
	case "iframe", "frame", "embed", "object":
		return false
	}
	return request.Header.Get("Forwarded") == "" && request.Header.Get("X-Forwarded-Host") == "" && request.Header.Get("X-Forwarded-Proto") == "" && request.Header.Get("X-Forwarded-For") == ""
}
