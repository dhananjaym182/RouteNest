package provideradapters_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"routenest/internal/app"
	"routenest/internal/provideradapters"
	"routenest/internal/providerreg"
)

type canceledTransport struct{}

func (canceledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestComposedRegistryCoversEveryDefinition(t *testing.T) {
	app.Configure()
	reg := provideradapters.Default()
	if got, want := reg.IDs(), []string{"cline", "openaicompat", "opencodezen", "qoder"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("adapter IDs = %v, want %v", got, want)
	}
	if err := reg.ValidateDefinitions(providerreg.Builtin()); err != nil {
		t.Fatal(err)
	}
}

func TestEveryInvokerPropagatesCancellation(t *testing.T) {
	app.Configure()
	client := &http.Client{Transport: canceledTransport{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	invocations := map[string]provideradapters.Invocation{
		"openaicompat": {
			HTTP: client, BaseURL: "https://provider.test/v1", APIKey: "key", Body: []byte(`{}`),
		},
		"cline": {
			HTTP: client, BaseURL: "https://cline.test/api/v1", SessionID: "task",
			Credential: provideradapters.Credential{Token: "token"}, Body: []byte(`{}`),
		},
		"opencodezen": {
			HTTP: client, BaseURL: "https://opencode.test/v1", APIKey: "public", Body: []byte(`{}`),
		},
		"qoder": {
			HTTP: client, BaseURL: "https://qoder.test",
			Credential: provideradapters.Credential{Token: "token", Metadata: map[string]string{"userId": "u", "machineId": "m"}},
			Params:     map[string]any{"model": "qoder/auto"},
		},
	}

	for id, invocation := range invocations {
		t.Run(id, func(t *testing.T) {
			bundle, ok := provideradapters.Default().Get(id)
			if !ok {
				t.Fatalf("adapter %q not registered", id)
			}
			resp, err := bundle.Invoker.Invoke(ctx, invocation)
			if resp != nil {
				resp.Body.Close()
				t.Fatalf("canceled invocation returned response: %+v", resp)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
}
