package cline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
	upstream "routenest/internal/upstream/cline"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Invoke(ctx context.Context, in provideradapters.Invocation) (*http.Response, error) {
	if in.HTTP == nil {
		return nil, fmt.Errorf("cline adapter: HTTP client is nil")
	}
	client := &upstream.Client{HTTP: in.HTTP, BaseURL: in.BaseURL, ExtraHeaders: in.Headers}
	return client.Chat(ctx, in.Credential.Token, in.SessionID, in.Body)
}

func (a *Adapter) ClassifyFailure(in provideradapters.FailureInput) []domain.AvailabilityObservation {
	return providerstate.DetectClineFailure(providerstate.HTTPFailure{
		Scope: in.Scope, StatusCode: in.StatusCode, Header: in.Header, Body: string(in.Body),
	})
}

func (a *Adapter) DiscoverModels(ctx context.Context, in provideradapters.DiscoveryInput) ([]modelcatalog.Entry, error) {
	if in.HTTP == nil {
		return nil, fmt.Errorf("cline adapter: HTTP client is nil")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(in.BaseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("cline adapter: build models request: %w", err)
	}
	if in.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+in.APIKey)
	}
	for key, value := range in.Headers {
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
	var payload struct {
		Recommended []modelcatalog.Entry `json:"recommended"`
		Free        []modelcatalog.Entry `json:"free"`
		ClinePass   []modelcatalog.Entry `json:"clinePass"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, modelcatalog.MaxCatalogBody)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	entries := make([]modelcatalog.Entry, 0, len(payload.Free)+len(payload.ClinePass)+len(payload.Recommended))
	for _, entry := range payload.Free {
		entry.FreeFlag = true
		entries = append(entries, entry)
	}
	entries = append(entries, payload.ClinePass...)
	entries = append(entries, payload.Recommended...)
	if len(entries) == 0 {
		return nil, modelcatalog.ErrNoModels
	}
	return entries, nil
}
