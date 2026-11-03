package cline

import (
	"context"
	"fmt"
	"net/http"

	"routenest/internal/provideradapters"
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
