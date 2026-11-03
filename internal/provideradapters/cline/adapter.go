package cline

import (
	"context"
	"fmt"
	"net/http"

	"routenest/internal/domain"
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
