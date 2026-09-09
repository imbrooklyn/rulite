// Package otel observes Rulite executions with caller-supplied OpenTelemetry
// providers. Adapter.Fire installs a private synchronous Observer for each call
// and delegates to the immutable Engine or Runtime. It does not change callback
// contexts, execution policy, or the complete business Result and Trace.
//
// Metrics use bounded attributes; rule IDs and business versions require
// explicit allowlists. Tracing uses one execution span and a bounded prefix of
// ordered rule events. Input, error text, panic values, source digests, and
// callback parameters are never exported by this package.
//
// Providers own aggregation, sampling, exporting, flushing, and shutdown.
// This package creates no providers, exporters, globals, or background workers.
package otel
