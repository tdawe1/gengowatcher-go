package gengo

import "context"

// Monitor defines the interface that all job monitors must implement.
// This is the core abstraction - WebSocket, RSS, Email, and Website monitors
// all implement this interface, allowing the watcher to treat them uniformly.
//
// The interface is intentionally minimal to keep implementations decoupled.
// Each monitor manages its own connection/polling lifecycle and emits
// JobEvent instances to the provided channel.
type Monitor interface {
	// Start begins monitoring for jobs. It blocks until the context is cancelled
	// or a fatal error occurs. Jobs are sent to the events channel.
	//
	// The monitor should:
	//   - Handle reconnection/retry logic internally
	//   - Emit EventError for non-fatal errors (monitor continues)
	//   - Return immediately when ctx is cancelled
	//   - Return non-nil error only for fatal errors that stop the monitor
	Start(ctx context.Context, events chan<- JobEvent) error

	// Name returns a human-readable identifier for this monitor.
	// Used in logging and UI display.
	Name() string

	// Source returns the source type this monitor produces.
	// Used for job metadata and statistics.
	Source() Source

	// Enabled returns whether this monitor is configured and should run.
	// If false, the watcher will skip starting this monitor.
	Enabled() bool
}
