//go:build desktop

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"routenest/internal/accounts"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const defaultDesktopPort = 3457

func main() {
	if handled, err := runAdminRecoveryCommand(os.Args[1:]); handled {
		if err != nil {
			log.Fatalf("Administrator recovery failed: %v", err)
		}
		return
	}
	selfCheck := flag.Bool("selfcheck", false, "Start embedded proxy, wait for /ready, and exit")
	port := flag.Int("port", configuredDesktopPort(), "Proxy server port")
	host := flag.String("host", "", "Proxy server listen host (0.0.0.0 = all interfaces)")
	initializeEmptyStore := flag.Bool("initialize-empty-account-store", false, "Authorize creation of an empty client-authentication store in a confirmed fresh data directory for this startup")
	flag.Parse()
	runtime, err := prepareDesktopRuntime(*host, *port, *initializeEmptyStore, *selfCheck)
	if err != nil {
		log.Fatalf("Desktop startup failed: %v", err)
	}
	defer func() {
		if err := runtime.close(); err != nil {
			log.Printf("Administrator security shutdown failed: %v", err)
		}
	}()

	if *selfCheck {
		fmt.Println(runtime.selfCheckMessage())
		return
	}

	if runtime.host != "127.0.0.1" && runtime.host != "localhost" && runtime.host != "::1" {
		log.Printf("警告: 监听 %s 非本机回环地址，桌面窗口仍只连接 127.0.0.1", runtime.host)
	}

	if err := runDesktopWindow(runtime); err != nil {
		log.Fatal(err)
	}
}

func configuredDesktopPort() int {
	if value, err := strconv.Atoi(os.Getenv("CLINE_PROXY_PORT")); err == nil && value > 0 && value < 65536 {
		return value
	}
	return defaultDesktopPort
}

func configuredDesktopHost() string {
	if value := os.Getenv("CLINE_PROXY_HOST"); value != "" {
		return value
	}
	if value := accounts.LoadPool().ListenHost; value != "" {
		return value
	}
	return "127.0.0.1"
}

func waitForEmbeddedProxy(adminURL, path string, timeout time.Duration, proxyErr <-chan error) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	url := strings.TrimSuffix(adminURL, "/admin/") + path
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case err := <-proxyErr:
			return fmt.Errorf("代理启动失败：%w", err)
		default:
		}

		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("probe %s returned status %d", path, resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("代理启动超时（%s）：%v", url, lastErr)
}

func runDesktopWindow(runtime *desktopRuntime) error {
	var contextMu sync.RWMutex
	var windowContext context.Context
	return wails.Run(&options.App{
		Title:     "RouteNest",
		Width:     1280,
		Height:    820,
		MinWidth:  960,
		MinHeight: 620,
		AssetServer: &assetserver.Options{
			Assets: desktopSetupAssets(),
		},
		Bind:             []interface{}{runtime.bridge},
		BackgroundColour: &options.RGBA{R: 248, G: 250, B: 252, A: 255},
		OnStartup: func(ctx context.Context) {
			contextMu.Lock()
			windowContext = ctx
			contextMu.Unlock()
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.routenest.desktop",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				contextMu.RLock()
				ctx := windowContext
				contextMu.RUnlock()
				if ctx != nil {
					wailsruntime.WindowUnminimise(ctx)
					wailsruntime.WindowShow(ctx)
				}
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
	})
}
