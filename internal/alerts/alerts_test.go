package alerts

import (
	"testing"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

func TestEvaluateTriggersOnBreach(t *testing.T) {
	e := NewEngine(
		models.AlertRule{Metric: "temperature", Operator: ">", Threshold: 80, Severity: models.SeverityCritical},
	)
	got := e.Evaluate(models.Telemetry{
		DeviceID:  "d1",
		Timestamp: time.Now(),
		Metrics:   map[string]float64{"temperature": 95},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
	if got[0].Severity != models.SeverityCritical {
		t.Fatalf("unexpected severity %s", got[0].Severity)
	}
	if got[0].DeviceID != "d1" {
		t.Fatalf("unexpected device %s", got[0].DeviceID)
	}
}

func TestEvaluateNoBreach(t *testing.T) {
	e := NewEngine(
		models.AlertRule{Metric: "temperature", Operator: ">", Threshold: 80, Severity: models.SeverityCritical},
	)
	got := e.Evaluate(models.Telemetry{Metrics: map[string]float64{"temperature": 50}})
	if len(got) != 0 {
		t.Fatalf("expected no alerts, got %d", len(got))
	}
}

func TestEvaluateMissingMetric(t *testing.T) {
	e := NewEngine(models.AlertRule{Metric: "battery", Operator: "<", Threshold: 10})
	got := e.Evaluate(models.Telemetry{Metrics: map[string]float64{"temperature": 5}})
	if len(got) != 0 {
		t.Fatalf("expected no alerts for missing metric, got %d", len(got))
	}
}

func TestOperators(t *testing.T) {
	cases := []struct {
		op        string
		value     float64
		threshold float64
		want      bool
	}{
		{">", 10, 5, true},
		{">", 5, 10, false},
		{">=", 5, 5, true},
		{"<", 1, 5, true},
		{"<=", 5, 5, true},
		{"==", 5, 5, true},
		{"==", 4, 5, false},
		{"??", 5, 5, false},
	}
	for _, c := range cases {
		if got := breached(c.value, c.op, c.threshold); got != c.want {
			t.Errorf("breached(%v %q %v) = %v, want %v", c.value, c.op, c.threshold, got, c.want)
		}
	}
}

func TestAddRuleAndRulesCopy(t *testing.T) {
	e := NewEngine()
	e.AddRule(models.AlertRule{Metric: "x", Operator: ">", Threshold: 1})
	rules := e.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	// Mutating the returned slice must not affect the engine.
	rules[0].Threshold = 999
	if e.Rules()[0].Threshold != 1 {
		t.Fatal("Rules() should return a defensive copy")
	}
}
