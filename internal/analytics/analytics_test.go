package analytics

import (
	"testing"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

func sample() []models.Telemetry {
	base := time.Now().UTC()
	return []models.Telemetry{
		{Timestamp: base.Add(2 * time.Second), Metrics: map[string]float64{"t": 30, "h": 40}},
		{Timestamp: base, Metrics: map[string]float64{"t": 10}},
		{Timestamp: base.Add(time.Second), Metrics: map[string]float64{"t": 20, "h": 60}},
	}
}

func TestAggregate(t *testing.T) {
	stats := Aggregate(sample())
	tStat, ok := stats["t"]
	if !ok {
		t.Fatal("missing metric t")
	}
	if tStat.Count != 3 {
		t.Fatalf("expected count 3, got %d", tStat.Count)
	}
	if tStat.Min != 10 || tStat.Max != 30 {
		t.Fatalf("min/max wrong: %+v", tStat)
	}
	if tStat.Avg != 20 {
		t.Fatalf("expected avg 20, got %v", tStat.Avg)
	}
	if tStat.Sum != 60 {
		t.Fatalf("expected sum 60, got %v", tStat.Sum)
	}

	hStat := stats["h"]
	if hStat.Count != 2 || hStat.Avg != 50 {
		t.Fatalf("h stats wrong: %+v", hStat)
	}
}

func TestAggregateEmpty(t *testing.T) {
	if got := Aggregate(nil); len(got) != 0 {
		t.Fatalf("expected empty stats, got %d", len(got))
	}
}

func TestTimeSeriesOrdered(t *testing.T) {
	series := TimeSeries(sample(), "t")
	if len(series) != 3 {
		t.Fatalf("expected 3 points, got %d", len(series))
	}
	// Should be sorted ascending by timestamp: values 10, 20, 30.
	if series[0].Value != 10 || series[1].Value != 20 || series[2].Value != 30 {
		t.Fatalf("series not ordered: %+v", series)
	}
	for i := 1; i < len(series); i++ {
		if series[i].Timestamp < series[i-1].Timestamp {
			t.Fatal("timestamps not ascending")
		}
	}
}

func TestTimeSeriesSkipsMissingMetric(t *testing.T) {
	series := TimeSeries(sample(), "h")
	if len(series) != 2 {
		t.Fatalf("expected 2 points for h, got %d", len(series))
	}
}
