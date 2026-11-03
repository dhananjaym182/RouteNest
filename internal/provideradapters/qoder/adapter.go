package qoder

import (
	"context"
	"net/http"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
	upstream "routenest/internal/upstream/qoder"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Invoke(ctx context.Context, in provideradapters.Invocation) (*http.Response, error) {
	client := in.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	return upstream.Chat(ctx, client, upstream.Creds{
		UserID:    in.Credential.Metadata["userId"],
		AuthToken: in.Credential.Token,
		Email:     in.Credential.Email,
		Name:      in.Credential.Name,
		MachineID: in.Credential.Metadata["machineId"],
		BaseURL:   in.BaseURL,
	}, in.Params, in.Stream)
}

func (a *Adapter) ClassifyFailure(in provideradapters.FailureInput) []domain.AvailabilityObservation {
	return providerstate.DetectStandardFailure(providerstate.HTTPFailure{
		Scope: in.Scope, StatusCode: in.StatusCode, Header: in.Header, Body: string(in.Body),
	})
}
