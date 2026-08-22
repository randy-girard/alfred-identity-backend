package metrics

import (
	"fmt"
	"time"
)

// RangeSpec describes a metrics query window and aggregation bucket.
type RangeSpec struct {
	ID     string
	Since  time.Duration
	Bucket time.Duration
}

// ParseRange maps UI timeframe ids to query parameters.
func ParseRange(id string) (RangeSpec, error) {
	switch id {
	case "1h":
		return RangeSpec{ID: "1h", Since: time.Hour, Bucket: time.Minute}, nil
	case "24h":
		return RangeSpec{ID: "24h", Since: 24 * time.Hour, Bucket: 5 * time.Minute}, nil
	case "7d":
		return RangeSpec{ID: "7d", Since: 7 * 24 * time.Hour, Bucket: time.Hour}, nil
	case "30d":
		return RangeSpec{ID: "30d", Since: 30 * 24 * time.Hour, Bucket: 6 * time.Hour}, nil
	case "90d":
		return RangeSpec{ID: "90d", Since: 90 * 24 * time.Hour, Bucket: 24 * time.Hour}, nil
	default:
		return RangeSpec{}, fmt.Errorf("invalid range")
	}
}

// DefaultRange is used when the client omits range.
func DefaultRange() RangeSpec {
	spec, _ := ParseRange("24h")
	return spec
}
