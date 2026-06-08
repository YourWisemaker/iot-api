// Package alerts evaluates telemetry against configured rules and emits alerts.
package alerts

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// Engine holds alert rules and evaluates telemetry against them.
type Engine struct {
	mu    sync.RWMutex
	rules []models.AlertRule
}

// NewEngine creates an alert engine with the provided rules.
func NewEngine(rules ...models.AlertRule) *Engine {
	return &Engine{rules: rules}
}

// AddRule registers a new alert rule.
func (e *Engine) AddRule(r models.AlertRule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, r)
}

// Rules returns a copy of the configured rules.
func (e *Engine) Rules() []models.AlertRule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]models.AlertRule, len(e.rules))
	copy(out, e.rules)
	return out
}

// Evaluate checks a telemetry sample against all rules and returns any alerts
// that were triggered.
func (e *Engine) Evaluate(t models.Telemetry) []models.Alert {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	var triggered []models.Alert
	for _, rule := range rules {
		value, ok := t.Metrics[rule.Metric]
		if !ok {
			continue
		}
		if breached(value, rule.Operator, rule.Threshold) {
			triggered = append(triggered, models.Alert{
				ID:        uuid.NewString(),
				DeviceID:  t.DeviceID,
				Metric:    rule.Metric,
				Value:     value,
				Threshold: rule.Threshold,
				Severity:  rule.Severity,
				Message: fmt.Sprintf("metric %q value %.2f %s threshold %.2f",
					rule.Metric, value, rule.Operator, rule.Threshold),
				CreatedAt: time.Now().UTC(),
			})
		}
	}
	return triggered
}

// breached reports whether value satisfies the operator against threshold.
func breached(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}
