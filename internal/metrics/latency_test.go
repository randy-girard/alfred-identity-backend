package metrics

import (
	"context"
	"testing"
)

func TestMeasurePingLatencyMSNilDB(t *testing.T) {
	if _, ok := MeasurePingLatencyMS(context.Background(), nil); ok {
		t.Fatal("expected nil db to fail")
	}
}
