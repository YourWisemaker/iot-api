// Package analytics computes aggregate statistics over historical telemetry.
package analytics

import (
	"sort"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// Aggregate computes per-metric statistics across a slice of telemetry points.
// The result is keyed by metric name.
func Aggregate(points []models.Telemetry) map[string]models.MetricStats {
	acc := make(map[string]*models.MetricStats)
	for _, p := range points {
		for metric, value := range p.Metrics {
			s, ok := acc[metric]
			if !ok {
				s = &models.MetricStats{
					Metric: metric,
					Min:    value,
					Max:    value,
				}
				acc[metric] = s
			}
			s.Count++
			s.Sum += value
			s.Last = value
			if value < s.Min {
				s.Min = value
			}
			if value > s.Max {
				s.Max = value
			}
		}
	}

	out := make(map[string]models.MetricStats, len(acc))
	for metric, s := range acc {
		if s.Count > 0 {
			s.Avg = s.Sum / float64(s.Count)
		}
		out[metric] = *s
	}
	return out
}

// TimeSeries extracts an ordered (timestamp, value) series for a single metric,
// sorted ascending by time. Points missing the metric are skipped.
type Point struct {
	Timestamp int64   `json:"timestamp"` // unix milliseconds
	Value     float64 `json:"value"`
}

// TimeSeries returns the values of a single metric ordered by time.
func TimeSeries(points []models.Telemetry, metric string) []Point {
	out := make([]Point, 0, len(points))
	for _, p := range points {
		if v, ok := p.Metrics[metric]; ok {
			out = append(out, Point{
				Timestamp: p.Timestamp.UnixMilli(),
				Value:     v,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp < out[j].Timestamp
	})
	return out
}
