package provideradapters

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"routenest/internal/providerreg"
)

type testInvoker struct{ err error }

func (t testInvoker) Invoke(context.Context, Invocation) (*http.Response, error) {
	return nil, t.err
}

func TestRegistryRejectsInvalidBundles(t *testing.T) {
	if _, err := NewRegistry(Bundle{}); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("empty bundle ID: err = %v", err)
	}
	if _, err := NewRegistry(Bundle{ID: "x"}); err == nil || !strings.Contains(err.Error(), "invoker") {
		t.Fatalf("nil invoker: err = %v", err)
	}
	if _, err := NewRegistry(
		Bundle{ID: "x", Invoker: testInvoker{}},
		Bundle{ID: "x", Invoker: testInvoker{}},
	); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate bundle: err = %v", err)
	}
}

func TestRegistryLookupAndDefinitionValidation(t *testing.T) {
	wantErr := errors.New("called")
	reg, err := NewRegistry(
		Bundle{ID: "openaicompat", Invoker: testInvoker{err: wantErr}},
		Bundle{ID: "qoder", Invoker: testInvoker{}},
		Bundle{ID: "cline", Invoker: testInvoker{}},
		Bundle{ID: "opencodezen", Invoker: testInvoker{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle, ok := reg.Get("openaicompat")
	if !ok {
		t.Fatal("missing registered adapter")
	}
	if _, err := bundle.Invoker.Invoke(context.Background(), Invocation{}); !errors.Is(err, wantErr) {
		t.Fatalf("invoker error = %v, want %v", err, wantErr)
	}
	if err := reg.ValidateDefinitions(providerreg.Builtin()); err != nil {
		t.Fatalf("built-in definitions rejected: %v", err)
	}

	missing, err := NewRegistry(Bundle{ID: "openaicompat", Invoker: testInvoker{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.ValidateDefinitions(providerreg.Builtin()); err == nil || !strings.Contains(err.Error(), "adapter") {
		t.Fatalf("missing definition adapter: err = %v", err)
	}
}

func TestPublishAndDefaultRegistry(t *testing.T) {
	reg, err := NewRegistry(Bundle{ID: "openaicompat", Invoker: testInvoker{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(reg); err != nil {
		t.Fatal(err)
	}
	if Default() != reg || !HasDefault() {
		t.Fatal("published registry is not the default")
	}
	if err := Publish(nil); err == nil {
		t.Fatal("publishing nil registry must fail")
	}
}
