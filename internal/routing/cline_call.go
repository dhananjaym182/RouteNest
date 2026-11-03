package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/platform"
	clineupstream "routenest/internal/upstream/cline"
)

func CallClineAPIWithAccount(acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	return CallClineAPIWithAccountCtx(context.Background(), acc, params, stream)
}

func CallClineAPIWithAccountCtx(ctx context.Context, acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	token, err := accounts.EnsureAccountToken(acc)
	if err != nil {

		return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s token failed: %w", acc.Email, err)}
	}

	body := BuildUpstreamBody(params, stream)
	sessionID, _ := body["session_id"].(string)

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, acc, fmt.Errorf("marshal body: %w", err)
	}

	client := &clineupstream.Client{
		HTTP:         &http.Client{Transport: egress.Client.Transport},
		BaseURL:      accounts.ClineAPIBase,
		ExtraHeaders: liveConfig().Headers,
	}

	toolCount := 0
	if tools, ok := params["tools"]; ok {
		if t, ok := tools.([]any); ok {
			toolCount = len(t)
		}
	}
	log.Printf("  upstream: account=%s stream=%v tools=%d msgs=%d max_tokens=%v effort=%v",
		TruncateEmail(acc.Email), stream, toolCount, getMsgCount(params), body["max_tokens"], body["reasoning_effort"])

	resp, err := client.Chat(ctx, token, sessionID, bodyJSON)
	if err != nil {
		acc.Status = "cooldown"
		acc.CooldownUntil = time.Now().Add(5 * time.Minute)
		accounts.SavePool()
		return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("upstream request: %w", err)}
	}

	if resp.StatusCode == 401 {
		resp.Body.Close()

		if err := accounts.RefreshAccountToken(acc); err == nil {
			token = acc.AccessToken
			resp, err = client.Chat(ctx, token, sessionID, bodyJSON)
			if err != nil {
				acc.Status = "cooldown"
				acc.CooldownUntil = time.Now().Add(5 * time.Minute)
				accounts.SavePool()
				return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("upstream retry: %w", err)}
			}
			if resp.StatusCode == 401 {
				resp.Body.Close()
				acc.Status = "expired"
				accounts.SavePool()
				return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s token expired permanently", acc.Email)}
			}
		} else {
			return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s refresh failed: %w", acc.Email, err)}
		}
	}

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(bodyBytes)

		if resp.StatusCode == 429 {
			model, _ := body["model"].(string)
			until := parseCooldownUntil(bodyStr)
			if model != "" {
				SetModelCooldown(acc, model, until)
			} else {
				acc.Status = "cooldown"
				acc.CooldownUntil = until
				accounts.SavePool()
			}
		}

		if model, _ := body["model"].(string); model != "" && IsModelGoneError(resp.StatusCode, bodyStr) {
			MarkModelGone(model)
		}
		return nil, acc, egress.NewAPIError(resp.StatusCode, platform.Truncate(bodyStr, 500))
	}

	acc.LastUsed = time.Now()
	acc.UsageCount++
	accounts.SavePool()
	return resp, acc, nil
}
