package dispatch

import (
	"context"
	"fmt"
	"sync/atomic"
)

// LegacyExecutor is the reversible compatibility path used by legacy and
// shadow modes. Shadow planning never calls it more than once per request.
type LegacyExecutor interface {
	ExecuteLegacy(context.Context, Request) (Result, error)
}

type Runtime struct {
	Executor *Executor
	Legacy   LegacyExecutor
}

var defaultRuntime atomic.Pointer[Runtime]

func PublishRuntime(runtime *Runtime) error {
	if runtime == nil || runtime.Executor == nil || runtime.Legacy == nil {
		return fmt.Errorf("dispatch: executor and legacy compatibility path are required")
	}
	defaultRuntime.Store(runtime)
	return nil
}

func DefaultRuntime() *Runtime {
	runtime := defaultRuntime.Load()
	if runtime == nil {
		panic("dispatch: app.Configure() was not called (runtime unset)")
	}
	return runtime
}

func HasRuntime() bool { return defaultRuntime.Load() != nil }
