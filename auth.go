package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	clineupstream "cline-go-proxy/internal/upstream/cline"
)

const (
	workosClientID        = clineupstream.DefaultClientID
	workosDeviceAuthURL   = clineupstream.DefaultDeviceAuthURL
	workosAuthenticateURL = clineupstream.DefaultAuthenticateURL
	clineAPIBase          = clineupstream.DefaultAPIBase
)

var clineAuthConfig = clineupstream.DefaultConfig()

type credentials struct {
	RefreshToken string `json:"refreshToken"`
}

// Type aliases keep existing call sites on the package implementations.
type deviceAuthResp = clineupstream.DeviceAuth
type authenticateResp = clineupstream.Authenticate
type clineAuthResp = clineupstream.AuthResponse
type clineRefreshResp = clineupstream.RefreshResponse
type refreshRejectedError = clineupstream.RefreshRejectedError

var (
	cachedToken      string
	cachedExpiry     int64
	cachedRefreshTok string
	credentialsPath  string
)

func init() {
	credentialsPath = findCredentialsFile()
}

func findCredentialsFile() string {
	// 数据目录环境变量（ROUTENEST_DATA_DIR，旧 CLINE2API_DATA_DIR 仍兜底）
	// 与 resolveDataPath 的 exe/工作目录/主目录启发式保持一致（含旧文件迁移）。
	return resolveDataPath(".routenest-credentials.json")
}

func loadCredentials() *credentials {
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

func saveCredentials(rt string) {
	c := credentials{RefreshToken: rt}
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := writeFileAtomic(credentialsPath, data, 0600); err != nil {
		log.Printf("Failed to save credentials: %v", err)
		return
	}
	log.Printf("Credentials saved to %s", credentialsPath)
}

func workosDeviceAuth() (*deviceAuthResp, error) {
	return clineupstream.DeviceAuthorization(context.Background(), httpClient, clineAuthConfig)
}

func pollWorkosToken(deviceCode string, interval, expiresIn int) (*authenticateResp, error) {
	return clineupstream.PollDeviceToken(context.Background(), httpClient, clineAuthConfig, deviceCode, interval, expiresIn)
}

func registerWithCline(workosAccess, workosRefresh string) (*clineAuthResp, error) {
	return clineupstream.Register(context.Background(), httpClient, clineAuthConfig, workosAccess, workosRefresh)
}

func isRefreshRejected(err error) bool { return clineupstream.IsRefreshRejected(err) }

func refreshClineToken(refreshToken string) (*clineRefreshResp, error) {
	return clineupstream.Refresh(context.Background(), httpClient, clineAuthConfig, refreshToken)
}

func refreshViaWorkOS(refreshToken string) *authenticateResp {
	return clineupstream.RefreshViaWorkOS(context.Background(), httpClient, clineAuthConfig, refreshToken)
}

func getToken() (string, error) {
	if cachedToken != "" && time.Now().UnixMilli() < cachedExpiry {
		return cachedToken, nil
	}

	creds := loadCredentials()
	if creds != nil && creds.RefreshToken != "" {
		resp, err := refreshClineToken(creds.RefreshToken)
		if err == nil && resp.Data.AccessToken != "" {
			cachedToken = "workos:" + resp.Data.AccessToken
			cachedRefreshTok = resp.Data.RefreshToken
			if cachedRefreshTok == "" {
				cachedRefreshTok = creds.RefreshToken
			}
			cachedExpiry = parseExpiry(resp.Data.ExpiresAt) - 60000
			saveCredentials(cachedRefreshTok)
			return cachedToken, nil
		}
		// cline 端点拒绝时走 WorkOS 通道自愈（refresh token 即 WorkOS token）
		if isRefreshRejected(err) {
			if wos := refreshViaWorkOS(creds.RefreshToken); wos != nil {
				cachedToken = "workos:" + wos.AccessToken
				cachedRefreshTok = wos.RefreshToken
				if cachedRefreshTok == "" {
					cachedRefreshTok = creds.RefreshToken
				}
				cachedExpiry = time.Now().Add(time.Hour).UnixMilli() - 60000
				saveCredentials(cachedRefreshTok)
				log.Printf("Token refreshed via WorkOS fallback")
				return cachedToken, nil
			}
		}
		log.Printf("Token refresh failed: %v", err)
	}
	return "", fmt.Errorf("no valid credentials. Run with --login flag first")
}

func parseExpiry(exp any) int64 { return clineupstream.ParseExpiry(exp) }

func doLogin() error {
	fmt.Println()
	fmt.Println("Starting Cline OAuth login...")
	fmt.Println()

	device, err := workosDeviceAuth()
	if err != nil {
		return err
	}

	authURL := device.VerificationURIComplete
	if authURL == "" {
		authURL = device.VerificationURI
	}

	fmt.Println("  1. Open this URL in your browser:")
	fmt.Println("     " + authURL)
	fmt.Println("  2. Enter code: " + device.UserCode)
	fmt.Println("  3. Log in with Google, GitHub, or email")
	fmt.Println()

	// Try to open browser automatically
	_ = openBrowser(authURL)

	fmt.Println("  Waiting for authorization...")

	interval := device.Interval
	if interval < 5 {
		interval = 5
	}
	expiresIn := device.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 300
	}

	workosTok, err := pollWorkosToken(device.DeviceCode, interval, expiresIn)
	if err != nil {
		return err
	}

	fmt.Println("  WorkOS authorized. Registering with Cline...")

	cline, err := registerWithCline(workosTok.AccessToken, workosTok.RefreshToken)
	if err != nil {
		return err
	}

	if cline.Data.RefreshToken == "" {
		return fmt.Errorf("cline registration missing refresh token")
	}

	saveCredentials(cline.Data.RefreshToken)
	cachedToken = "workos:" + cline.Data.AccessToken
	cachedRefreshTok = cline.Data.RefreshToken
	cachedExpiry = parseExpiry(cline.Data.ExpiresAt) - 60000

	email := "unknown"
	if cline.Data.UserInfo != nil && cline.Data.UserInfo.Email != "" {
		email = cline.Data.UserInfo.Email
	}
	fmt.Printf("  Login successful! Account: %s\n", email)
	return nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch {
	case isWindows():
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		// Try common browser openers
		for _, candidate := range []string{"xdg-open", "open", "gnome-open"} {
			if _, err := os.Stat("/usr/bin/" + candidate); err == nil {
				cmd = candidate
				break
			}
			if _, err := os.Stat("/usr/local/bin/" + candidate); err == nil {
				cmd = candidate
				break
			}
		}
	}

	if cmd == "" {
		return fmt.Errorf("no browser opener found")
	}

	return runCommand(cmd, args...)
}

func isWindows() bool {
	return strings.Contains(strings.ToLower(os.Getenv("OS")), "windows")
}
