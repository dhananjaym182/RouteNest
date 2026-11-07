package admin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"routenest/internal/adminauth"
	"routenest/internal/platform"
	storeadminauth "routenest/internal/store/adminauth"
)

// RecoverSecurityOffline is called only by the pre-server local command. It has
// no HTTP adapter and accepts no caller-supplied password or delivery channel.
func RecoverSecurityOffline(ctx context.Context) error {
	tty, err := openControllingTerminal()
	if err != nil {
		return fmt.Errorf("offline administrator recovery requires a controlling local terminal: %w", err)
	}
	defer tty.Close()
	dir, err := recoveryDataDir()
	if err != nil {
		return err
	}
	return recoverSecurityOffline(ctx, dir, func(secret string) error {
		_, err := fmt.Fprintf(tty, "\nRouteNest one-time administrator recovery secret:\n\n  %s\n\nRestart RouteNest, open the local admin console, and use this secret to set a permanent password. All administrator sessions were revoked. This secret will not be shown again.\n\n", secret)
		return err
	})
}

// Resolve without CurrentDataDir: normal startup creates/tightens permissions,
// which must not precede the offline ownership and permission checks.
func recoveryDataDir() (string, error) {
	for _, env := range []string{platform.DataDirEnv, platform.LegacyDataDirEnv} {
		if dir := strings.TrimSpace(os.Getenv(env)); dir != "" {
			return filepath.Abs(dir)
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve recovery data directory: %w", err)
	}
	return filepath.Abs(filepath.Join(base, "routenest"))
}

func recoverSecurityOffline(ctx context.Context, dataDir string, deliver func(string) error) (err error) {
	if deliver == nil {
		return fmt.Errorf("offline recovery requires private delivery")
	}
	repo, err := storeadminauth.OpenForRecovery(dataDir)
	if err != nil {
		if errors.Is(err, storeadminauth.ErrLocked) {
			return fmt.Errorf("stop every RouteNest process before administrator recovery: %w", err)
		}
		return err
	}
	defer func() { err = errors.Join(err, repo.Close()) }()
	return adminauth.RecoverOffline(ctx, repo, adminauth.Options{DeliverBootstrap: deliver})
}
