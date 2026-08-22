package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/alfred-identity/web/internal/metrics"
	"github.com/alfred-identity/web/internal/store"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	rangeID := strings.TrimSpace(r.URL.Query().Get("range"))
	if rangeID == "" {
		rangeID = metrics.DefaultRange().ID
	}
	spec, err := metrics.ParseRange(rangeID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_range")
		return
	}
	if s.store == nil {
		writeErr(w, http.StatusServiceUnavailable, "store_unavailable")
		return
	}
	ctx := r.Context()
	since := time.Now().UTC().Add(-spec.Since)
	series, err := s.store.QueryMetricSeries(ctx, since, spec.Bucket)
	if err != nil {
		if s.log != nil {
			s.log.Warn("metrics query", "err", err)
		}
		writeErr(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"range":          spec.ID,
		"bucket_seconds": int(spec.Bucket.Seconds()),
		"since":          since.Format(time.RFC3339),
		"series":         formatMetricSeries(series),
		"current":        s.currentMetrics(ctx),
	})
}

func formatMetricSeries(series map[string][]store.MetricPoint) map[string][]map[string]any {
	out := make(map[string][]map[string]any, len(series))
	for name, pts := range series {
		arr := make([]map[string]any, len(pts))
		for i, p := range pts {
			arr[i] = map[string]any{
				"t": p.T.Format(time.RFC3339),
				"v": p.V,
			}
		}
		out[name] = arr
	}
	return out
}

func (s *Server) currentMetrics(ctx context.Context) map[string]any {
	out := map[string]any{}
	if s.hub != nil {
		out[store.MetricGUIConnections] = len(s.hub.Connections())
	}
	if s.presence != nil {
		out[store.MetricGameSessions] = s.presence.Count()
	}
	if s.store != nil && s.store.DB != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		start := time.Now()
		if err := s.store.DB.PingContext(pingCtx); err == nil {
			out[store.MetricDBLatencyMS] = float64(time.Since(start).Milliseconds())
		}
		st := s.store.DB.Stats()
		out[store.MetricDBOpenConns] = st.OpenConnections
		out[store.MetricDBInUseConns] = st.InUse
		out[store.MetricDBIdleConns] = st.Idle
	}
	return out
}
