package openaicompat

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
	upstream "routenest/internal/upstream/openaicompat"
)

type BodyOptionsProvider func() upstream.BodyOptions

type Adapter struct {
	bodyOptions BodyOptionsProvider
}

func New(providers ...BodyOptionsProvider) *Adapter {
	adapter := &Adapter{}
	if len(providers) > 0 {
		adapter.bodyOptions = providers[0]
	}
	return adapter
}

func (a *Adapter) Invoke(ctx context.Context, in provideradapters.Invocation) (*http.Response, error) {
	if in.HTTP == nil {
		return nil, fmt.Errorf("openaicompat adapter: HTTP client is nil")
	}
	body := in.Body
	if len(body) == 0 && in.Params != nil {
		opts := upstream.BodyOptions{}
		if a.bodyOptions != nil {
			opts = a.bodyOptions()
		}
		if opts.Logf == nil {
			opts.Logf = log.Printf
		}
		var err error
		body, err = upstream.PrepareBody(in.Params, in.Stream, opts)
		if err != nil {
			return nil, fmt.Errorf("openaicompat adapter: %w", err)
		}
	}
	client := &upstream.Client{HTTP: in.HTTP, BaseURL: in.BaseURL, APIKey: in.APIKey, Headers: in.Headers}
	req, err := client.BuildRequest(body, upstream.RequestOptions{Token: in.Credential.Token, Headers: in.Credential.Headers})
	if err != nil {
		return nil, fmt.Errorf("openaicompat adapter: build request: %w", err)
	}
	req = req.WithContext(ctx)
	return in.HTTP.Do(req)
}

func (a *Adapter) ClassifyFailure(in provideradapters.FailureInput) []domain.AvailabilityObservation {
	return providerstate.DetectStandardFailure(providerstate.HTTPFailure{
		Scope: in.Scope, StatusCode: in.StatusCode, Header: in.Header, Body: string(in.Body),
	})
}
