package openaicompat

import (
	"context"
	"fmt"
	"net/http"

	"routenest/internal/provideradapters"
	upstream "routenest/internal/upstream/openaicompat"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Invoke(ctx context.Context, in provideradapters.Invocation) (*http.Response, error) {
	if in.HTTP == nil {
		return nil, fmt.Errorf("openaicompat adapter: HTTP client is nil")
	}
	client := &upstream.Client{HTTP: in.HTTP, BaseURL: in.BaseURL, APIKey: in.APIKey, Headers: in.Headers}
	req, err := client.BuildRequest(in.Body, upstream.RequestOptions{Token: in.Credential.Token, Headers: in.Credential.Headers})
	if err != nil {
		return nil, fmt.Errorf("openaicompat adapter: build request: %w", err)
	}
	req = req.WithContext(ctx)
	return in.HTTP.Do(req)
}
