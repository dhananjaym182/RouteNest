package opencodezen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
	upstream "routenest/internal/upstream/zen"
)

type Adapter struct {
	mu       sync.Mutex
	ids      *upstream.Identifier
	sessions *upstream.SessionCache
	pool     []string
}

func (a *Adapter) ClassifyFailure(in provideradapters.FailureInput) []domain.AvailabilityObservation {
	return providerstate.DetectZenFailure(providerstate.HTTPFailure{
		Scope: in.Scope, StatusCode: in.StatusCode, Header: in.Header, Body: string(in.Body),
	})
}

func New() *Adapter {
	return &Adapter{ids: upstream.NewIdentifier(), sessions: upstream.NewSessionCache()}
}

func (a *Adapter) setSessionPool(ids []string) {
	if ids == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if slices.Equal(a.pool, ids) {
		return
	}
	a.pool = append(a.pool[:0], ids...)
	a.sessions.SetPool(ids)
}

func (a *Adapter) Invoke(ctx context.Context, in provideradapters.Invocation) (*http.Response, error) {
	if in.HTTP == nil {
		return nil, fmt.Errorf("opencodezen adapter: HTTP client is nil")
	}
	if in.SessionID != "" {
		a.setSessionPool([]string{in.SessionID})
	} else {
		a.setSessionPool(in.SessionIDs)
	}
	protocol := in.Protocol
	if protocol == "" {
		protocol = upstream.ProtocolChat
	}
	client := &upstream.Client{
		HTTP: in.HTTP, BaseURL: in.BaseURL, APIKey: in.APIKey, Headers: in.Headers,
		IDs: a.ids, Sessions: a.sessions,
	}
	req, err := client.BuildRequest(ctx, protocol, in.Body, in.Params, in.ModelID)
	if err != nil {
		return nil, fmt.Errorf("opencodezen adapter: build request: %w", err)
	}
	return in.HTTP.Do(req)
}

func (a *Adapter) DiscoverModels(ctx context.Context, in provideradapters.DiscoveryInput) ([]modelcatalog.Entry, error) {
	if in.HTTP == nil {
		return nil, fmt.Errorf("opencodezen adapter: HTTP client is nil")
	}
	sessionID := a.ids.New("ses", true)
	requestID := a.ids.New("msg", false)
	endpoint := strings.TrimRight(in.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("opencodezen adapter: build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+in.APIKey)
	req.Header.Set("User-Agent", "opencode/"+upstream.ClientVersion)
	req.Header.Set("x-opencode-project", "global")
	req.Header.Set("x-opencode-session", sessionID)
	req.Header.Set("x-opencode-client", "cli")
	for key, value := range in.Headers {
		switch value {
		case "$session":
			value = sessionID
		case "$request":
			value = requestID
		case "$project":
			value = "global"
		case "$client":
			value = "cli"
		}
		if strings.TrimSpace(value) == "" {
			req.Header.Del(key)
		} else {
			req.Header.Set(key, value)
		}
	}
	resp, err := in.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models API returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, modelcatalog.MaxCatalogBody))
	if err != nil {
		return nil, err
	}
	return modelcatalog.ParseModels(body)
}
