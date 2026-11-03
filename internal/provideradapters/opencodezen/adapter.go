package opencodezen

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"routenest/internal/domain"
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
	a.setSessionPool(in.SessionIDs)
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
