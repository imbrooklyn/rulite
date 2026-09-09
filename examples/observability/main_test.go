package main

import (
	"bytes"
	"context"
	"testing"
)

func TestInMemoryObservation(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	want := "Discount: 20%; applied by: pricing/offer; version: pricing/v1; revision: 1\nFired: 1; telemetry diagnostics: 0\nFinished executions: 1; spans: 1; rule events: 3\n"
	if output.String() != want {
		t.Fatalf("output=%q; want %q", output.String(), want)
	}
}
