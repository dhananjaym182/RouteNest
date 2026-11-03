package qoder

import (
	"context"
	"strings"
	"testing"

	"routenest/internal/provideradapters"
)

func TestAdapterRejectsMissingModelWithoutLeakingCredential(t *testing.T) {
	const secret = "dt-super-secret"
	_, err := New().Invoke(context.Background(), provideradapters.Invocation{
		BaseURL: "https://qoder.invalid",
		Credential: provideradapters.Credential{
			Token:    secret,
			Metadata: map[string]string{"userId": "u", "machineId": "m"},
		},
		Params: map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing a model") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked credential: %v", err)
	}
}
