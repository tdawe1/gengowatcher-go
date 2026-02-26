package reaction

import (
	"context"
	"errors"
)

const defaultMaxInFlight = 64

var ErrAsyncBackpressure = errors.New("reaction async pool saturated")

type Deps struct {
	Open   func(context.Context, string) error
	Notify func(context.Context, string) error

	MaxInFlight int

	OnAsyncError func(action string, err error)
	OnAsyncPanic func(action string, recovered any)
}

type Executor struct {
	deps  Deps
	slots chan struct{}
}

func New(deps Deps) *Executor {
	maxInFlight := deps.MaxInFlight
	if maxInFlight <= 0 {
		maxInFlight = defaultMaxInFlight
	}

	return &Executor{
		deps:  deps,
		slots: make(chan struct{}, maxInFlight),
	}
}

func (e *Executor) Dispatch(ctx context.Context, url string, title string) {
	if e == nil {
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}

	e.dispatchAsync(ctx, "open", func(runCtx context.Context) error {
		if e.deps.Open == nil {
			return nil
		}

		return e.deps.Open(runCtx, url)
	})

	e.dispatchAsync(ctx, "notify", func(runCtx context.Context) error {
		if e.deps.Notify == nil {
			return nil
		}

		return e.deps.Notify(runCtx, title)
	})
}

func (e *Executor) dispatchAsync(ctx context.Context, action string, run func(context.Context) error) {
	if run == nil {
		return
	}

	select {
	case e.slots <- struct{}{}:
	default:
		e.reportError(action, ErrAsyncBackpressure)
		return
	}

	go func() {
		defer func() {
			<-e.slots
		}()

		defer func() {
			if recovered := recover(); recovered != nil {
				e.reportPanic(action, recovered)
			}
		}()

		if err := run(ctx); err != nil {
			e.reportError(action, err)
		}
	}()
}

func (e *Executor) reportError(action string, err error) {
	if e.deps.OnAsyncError == nil {
		return
	}

	defer func() {
		_ = recover()
	}()

	e.deps.OnAsyncError(action, err)
}

func (e *Executor) reportPanic(action string, recovered any) {
	if e.deps.OnAsyncPanic == nil {
		return
	}

	defer func() {
		_ = recover()
	}()

	e.deps.OnAsyncPanic(action, recovered)
}
