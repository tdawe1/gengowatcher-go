package reaction

import "context"

type Deps struct {
	Open   func(context.Context, string) error
	Notify func(context.Context, string) error

	OnAsyncError func(action string, err error)
	OnAsyncPanic func(action string, recovered any)
}

type Executor struct {
	deps Deps
}

func New(deps Deps) *Executor {
	return &Executor{deps: deps}
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

	go func() {
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
