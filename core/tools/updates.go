package tools

import "context"

type updatesKey struct{}

// WithUpdates returns a context in which a running tool can report partial output with Update. The agent uses it to turn the reports into
// tool_execution_update events.
func WithUpdates(ctx context.Context, fn func(partial string)) context.Context {
	return context.WithValue(ctx, updatesKey{}, fn)
}

// Updates is the function WithUpdates installed, or nil when nobody listens.
func Updates(ctx context.Context) func(partial string) {
	fn, _ := ctx.Value(updatesKey{}).(func(string))
	return fn
}

// Update reports partial output of the tool running under ctx. It does nothing when nobody listens.
func Update(ctx context.Context, partial string) {
	if fn := Updates(ctx); fn != nil {
		fn(partial)
	}
}
