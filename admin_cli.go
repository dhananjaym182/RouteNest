package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"routenest/internal/admin"
	auth "routenest/internal/adminauth"
	storeadminauth "routenest/internal/store/adminauth"
)

const adminAuthCommands = `Administrator authentication commands:
  routenest auth status  Show administrator authentication status without changing state
  routenest auth recover  Start owner-controlled offline emergency recovery
  routenest auth recover --help  Show recovery requirements without changing state
  routenest auth help  Show administrator password and recovery guidance`

const adminPasswordGuidance = `Administrator password and recovery:
  CLI/server (go build .): starts a server, not a native desktop window.
    Run routenest, then open http://localhost:3457/admin/ by default,
    or use your configured host/port.
  Create password: for a confirmed fresh CLI/server data directory only, start once with
    routenest -initialize-empty-account-store. Enter the one-time bootstrap secret from
    the controlling terminal in the browser and set the permanent admin password.
    The secret is printed in the terminal or console window that started RouteNest (Windows,
    macOS and Linux); restarting before setup completes prints a new one.
    Headless first startup (Docker, a service, no terminal) requires
    ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD supplied privately;
    this bootstrap secret does not reset an existing password.
    Setup also issues the first recovery code, shown once: save it before continuing.
  Native desktop: go build -tags "desktop production" -o routenest-desktop .
    Open routenest-desktop. Confirmed fresh installations show native password setup,
    recovery-code saving, and optional device trust.
    Native setup needs no terminal bootstrap secret or environment variable.
  Change password: sign in, then use Administrator Security -> Change password.
  Recovery code: first-run setup (browser or native desktop) issues one, shown once. Use
    Administrator Security -> Recovery code to generate or rotate a code
    after current-password reauthentication; save the code when it is shown once.
    Without a recovery code, "Forgot password?" on the sign-in screen explains how to recover.
  Forgot password: select "Forgot password?" on the sign-in screen and use the saved recovery code
    to reset the local administrator admin password; save the replacement code shown once.
    Other local users should ask a security administrator for a password reset.
    Password changes and using a recovery code to reset a forgotten password revoke
    sessions and trusted desktop devices; generating or rotating only a recovery code does not.
  Trusted Devices: use Administrator Security -> Desktop Sign-In to manage device trust,
    automatic sign-in, and revocation. Trusting, resuming, revoking, or forgetting
    registered devices requires the current local password. Native/Desktop Sign Out pauses
    automatic sign-in; ordinary browser logout only ends its browser session and does not
    pause or revoke native device trust. Forget this device removes its trust and local credential.
    Ordinary browsers require independent administrator authentication;
    they do not use native desktop device credentials.
  Lost recovery code: stop every RouteNest process, then run routenest auth recover as the
    data-directory owner in a local controlling terminal; intact authority is required.
    Restart RouteNest and finish recovery in the admin UI with the terminal-issued secret.
    Run routenest auth recover --help before emergency recovery.

--login (CLI/server only) runs provider OAuth and adds a provider account; it is not administrator login.
Passwords are entered in the administrator UI; there are no plaintext password flags.
-initialize-empty-account-store authorizes a confirmed fresh client store only; it is not a password-reset mechanism.`

const adminAuthUsage = `RouteNest administrator authentication

Usage:
  routenest auth <command>

` + adminAuthCommands + "\n\n" + adminPasswordGuidance

const adminStatusUsage = `Usage: routenest auth status

Validates the existing administrator snapshot and audit journal without changing files,
permissions, or locks. Works while RouteNest is running. Reports authentication mode,
active sessions, enabled trusted desktop devices, and recovery availability without secrets.

Missing, corrupt, or changing authority is not assumed to be fresh. Prepared transactions
require a RouteNest restart; status never completes them.
-initialize-empty-account-store is not a password-reset mechanism.`

const adminRecoverUsage = `Usage: routenest auth recover

Starts offline emergency administrator recovery. Stop every RouteNest process and run
this command as the data-directory owner in a local controlling terminal. The command
requires intact administrator authority, revokes sessions and trusted desktop devices,
and prints a one-time recovery secret privately.

This is distinct from ordinary password change or recovery-code reset.
-initialize-empty-account-store is not a password-reset mechanism.`

func runAdminAuthCommand(args []string, stdout io.Writer) (bool, error) {
	if len(args) == 0 || args[0] != "auth" {
		return false, nil
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if len(args) == 1 || args[1] == "help" || args[1] == "--help" || args[1] == "-h" {
		if len(args) > 2 {
			return true, fmt.Errorf("usage: routenest auth --help")
		}
		_, err := fmt.Fprintln(stdout, adminAuthUsage)
		return true, err
	}
	switch args[1] {
	case "status":
		if len(args) == 3 && (args[2] == "--help" || args[2] == "-h") {
			_, err := fmt.Fprintln(stdout, adminStatusUsage)
			return true, err
		}
		if len(args) != 2 {
			return true, fmt.Errorf("usage: routenest auth status")
		}
		return true, writeAdminAuthStatus(context.Background(), stdout)
	case "recover":
		if len(args) == 3 && (args[2] == "--help" || args[2] == "-h") {
			_, err := fmt.Fprintln(stdout, adminRecoverUsage)
			return true, err
		}
		if len(args) != 2 {
			return true, fmt.Errorf("usage: routenest auth recover (stop RouteNest first; run as the data-directory owner in a local terminal)")
		}
		return true, admin.RecoverSecurityOffline(context.Background())
	default:
		return true, fmt.Errorf("unknown auth command %q; run routenest auth --help", args[1])
	}
}

func writeAdminAuthStatus(ctx context.Context, stdout io.Writer) error {
	dataDir, err := adminAuthDataDir()
	if err != nil {
		return err
	}
	state, err := storeadminauth.Inspect(ctx, dataDir)
	if err != nil {
		status := "unavailable (authority could not be verified)"
		switch {
		case errors.Is(err, storeadminauth.ErrPrepared):
			status = "prepared transaction; restart required"
		case errors.Is(err, storeadminauth.ErrMissing):
			status = "authority missing or incomplete; fresh installation cannot be confirmed"
		case errors.Is(err, storeadminauth.ErrCorrupt):
			status = "corrupt authority; restore intact state"
		case errors.Is(err, storeadminauth.ErrUncertain):
			status = "uncertain while authority is changing; retry status"
		}
		_, outputErr := fmt.Fprintf(stdout, "Administrator authentication: %s\nActive sessions: unknown\nTrusted desktop devices: unknown\nRecovery code: unknown\nOffline owner recovery: unavailable until authority is verified\n", status)
		return errors.Join(err, outputErr)
	}
	return writeAdminAuthStateStatus(stdout, state, time.Now())
}

func writeAdminAuthStateStatus(stdout io.Writer, state auth.State, now time.Time) error {
	status := "configured"
	recovery := "available after stopping every RouteNest process; requires the data-directory owner and a local terminal"
	if !state.Initialized {
		status = "bootstrap required"
		recovery = "unavailable before administrator setup"
	} else if state.RecoveryPHC != "" {
		status = "offline recovery completion required"
	}
	ready := state.Initialized && state.RecoveryPHC == ""
	sessions := 0
	for _, session := range state.Sessions {
		user, exists := state.Users[session.UserID]
		if !ready || !exists || user.ID != session.UserID || user.Disabled || user.Revision != session.UserRevision || now.Before(session.IssuedAt) || !session.ExpiresAt.After(now) {
			continue
		}
		if session.DeviceID != "" {
			device, exists := state.TrustedDevices[session.DeviceID]
			if !exists || now.Before(device.CreatedAt) || device.ActivatedAt.After(now) || device.LastUsedAt.After(now) || !device.ExpiresAt.After(now) {
				continue
			}
		}
		sessions++
	}
	devices := 0
	for id, device := range state.TrustedDevices {
		user, exists := state.Users[device.UserID]
		if !ready || !exists || id != device.ID || user.ID != device.UserID || user.Disabled || user.Revision != device.UserRevision || user.PasswordPHC == "" || user.Legacy != nil || user.Provider != "" {
			continue
		}
		if !device.AutomaticSignIn || device.ActivatedAt.IsZero() || device.ActivatedAt.After(now) || now.Before(device.CreatedAt) || device.LastUsedAt.After(now) || !device.ExpiresAt.After(now) {
			continue
		}
		if _, err := auth.ParsePHC(user.PasswordPHC); err == nil {
			devices++
		}
	}
	_, err := fmt.Fprintf(stdout, "Administrator authentication: %s\nActive sessions: %d\nTrusted desktop devices: %d (enabled, activated, unexpired)\nRecovery code: %s\nOffline owner recovery: %s\n", status, sessions, devices, enabledLabel(ready && state.RecoveryCodePHC != ""), recovery)
	return err
}

func adminAuthDataDir() (string, error) {
	return admin.SecurityDataDir()
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "configured"
	}
	return "not configured"
}

func configureRootHelp(command *flag.FlagSet) {
	command.Usage = func() {
		_, _ = fmt.Fprintf(command.Output(), "Usage: routenest [options]\n       routenest auth <status|recover|help>\n\n%s\n\n%s\n\nOptions:\n", adminAuthCommands, adminPasswordGuidance)
		command.PrintDefaults()
	}
}
