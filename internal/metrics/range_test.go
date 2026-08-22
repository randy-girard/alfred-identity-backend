package metrics

import (
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	cases := map[string]time.Duration{
		"1h":  time.Minute,
		"24h": 5 * time.Minute,
		"7d":  time.Hour,
		"30d": 6 * time.Hour,
		"90d": 24 * time.Hour,
	}
	for id, bucket := range cases {
		spec, err := ParseRange(id)
		if err != nil || spec.ID != id || spec.Bucket != bucket {
			t.Fatalf("%s: spec=%+v err=%v", id, spec, err)
		}
	}
	if _, err := ParseRange("bad"); err == nil {
		t.Fatal("expected invalid range")
	}
}
