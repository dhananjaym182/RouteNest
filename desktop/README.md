# RouteNest Desktop

Single-file cross-platform desktop build (Wails v2): the proxy service, admin dashboard, and system WebView window are all contained in the same executable.

## Supported Platforms

| Platform | WebView | Prerequisites |
|------|---------|----------|
| Windows x64 | WebView2 | Bundled with Win10/11, no extra installation needed |
| macOS (Intel/Apple Silicon) | System WebKit | Xcode Command Line Tools (`xcode-select --install`) |
| Linux x64 | WebKit2GTK | GTK3 + WebKit2GTK development packages |

## For End Users

1. Extract the release package and open RouteNest normally. On a confirmed fresh data
   directory, the native setup window authorizes client-authentication storage creation
   without a terminal, environment variable, or copied bootstrap secret.
2. Create the local administrator password, save the one-time recovery code, and open
   the dashboard. The setup bridge becomes inert after durable initialization.
3. Connect a provider from the Welcome checklist, verify models, and create a client API
   key when authenticated client access is desired.
4. Clients use the local address:
   - Base URL: `http://127.0.0.1:3457/v1`
   - API Key: generate it in the admin dashboard
   - Model: e.g. `cline-free/glm-5.2`
5. Closing the desktop window terminates the entire program and the local proxy.

Use **Forgot Password?** with the saved recovery code to set a new administrator
password. Successful recovery revokes administrator sessions, invalidates the used code,
and shows one replacement code once. If every recovery credential is lost, use the
offline owner-terminal procedure in `docs/admin-security.md`; localhost access alone is
never recovery authority.

By default the program listens only on `127.0.0.1` and never exposes the admin dashboard to the LAN or the public internet.

## Building for Each Platform

Wails depends on C bindings for each platform's native WebView and **cannot cross-compile**; you must build separately on each target system:

### Windows (can be done on the current machine)

```bash
./desktop/build.sh
# Output: desktop/build/windows-amd64/routenest-desktop.exe
```

### Windows resource info (icon / version / manifest)

The Windows exe's icon, file version info (the "Details" property page), and application manifest (DPI awareness, Win10/11 compatibility declarations)
all come from the PE resource file `resource_windows_amd64.syso` (in the project root, automatically linked by go build).

After changing the icon or version number, regenerate it:

```bash
cd desktop/icon-gen
go run .            # Generates desktop/icon-gen/resource_windows_amd64.syso
cp resource_windows_amd64.syso ../../resource_windows_amd64.syso
```

The version number is changed in the `appVersion` and other constants at the top of `desktop/icon-gen/main.go`. The generated `.syso` should be committed to the repo;
CI builds depend on it (see `!resource_windows_amd64.syso` in the root `.gitignore`).

### Release zip (recommended for cloud-drive distribution)

Uploading a bare exe to a cloud drive is easily blocked by Chrome/Edge; packaging it as a zip significantly reduces the block rate:

```bash
./desktop/build.sh && ./desktop/dist.sh
# Output: desktop/dist/routenest-windows-amd64.zip (exe + usage instructions)
```

### Self-signing (optional, free tamper protection)

A self-signed certificate **cannot** eliminate SmartScreen blocking (browsers don't trust self-signed roots), but it makes the file properties show the signer,
prevents tampering, and some security software is friendlier toward "signed files":

```powershell
# Administrator PowerShell
powershell -ExecutionPolicy Bypass -File desktop/sign-self.ps1
```

### macOS

```bash
xcode-select --install   # needed on first use
./desktop/build.sh
# Output: desktop/build/darwin-arm64/routenest-desktop (Apple Silicon)
#         desktop/build/darwin-amd64/routenest-desktop (Intel)
```

Official distribution also requires signing and notarization:

```bash
codesign --deep --force --sign "Developer ID Application: <your-name>" routenest-desktop
xcrun notarytool submit routenest-desktop --apple-id <your-apple-id> --wait
```

### Linux

```bash
# Debian/Ubuntu
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
# Fedora
sudo dnf install gtk3-devel webkit2gtk4.1-devel
# Arch
sudo pacman -S gtk3 webkit2gtk

./desktop/build.sh
# Output: desktop/build/linux-amd64/routenest-desktop
```

## Debug Builds

Build a version with console output (does not hide the command-line window):

```bash
go build -tags "desktop production" -o routenest-desktop-debug.exe .
```

Self-check mode starts the proxy, verifies `/ready`, and exits without opening a
window. A fresh self-check needs the same explicit account-store authorization and a
test-only administrator bootstrap value:

```powershell
$env:ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD = "local-selfcheck-only-bootstrap"
.\routenest-desktop.exe -selfcheck -initialize-empty-account-store
```

## Technical Notes

- Builds must include `-tags "desktop production"`. A plain `go build` enters Wails' guard branch and errors out.
- A plain `go build .` builds the RouteNest CLI; the `desktop` build tag selects the desktop entry point.
- Accounts, API keys, request logs, and capture output use RouteNest's normal OS user-config directory (or `ROUTENEST_DATA_DIR`). Do not bundle `.routenest-*.json`, `capture-logs/`, or legacy `.cline-*.json` credential files into a release package.
- The `CLINE_PROXY_PORT` environment variable or the `-port` flag can override the default port `3457`.
- The Windows icon, version info, and application manifest are embedded via the `.syso` PE resource, generated by `desktop/icon-gen/` (icon + version info + manifest in the same resource file).
- `desktop_main.go` is one of two thin root entry points. Shared composition is initialized by `internal/app`, and the embedded server starts through `internal/proxycore.Start`.
- The first-run page is served from the embedded Wails asset origin and calls only the
  bound `DesktopSetupBridge`. Administrator creation is not exposed as a generic
  unauthenticated localhost endpoint. A two-minute single-use loopback handoff transfers
  the initial durable session to the existing `/admin/` UI.
- Existing or uncertain data directories never enter native setup. Missing, corrupt, or
  partially restored authentication state opens a fail-closed recovery screen instead.
- `-initialize-empty-account-store` remains an advanced self-check/headless-compatible
  override and is not required for normal desktop installation.
