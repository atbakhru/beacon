package beacon

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const DemoOrg = "b0000000-0000-4000-8000-000000000001"

type AlertInput struct {
	SourceEventID string    `json:"source_event_id"`
	ServiceID     string    `json:"service_id"`
	Signature     string    `json:"signature"`
	Severity      string    `json:"severity"`
	Summary       string    `json:"summary"`
	SeenAt        time.Time `json:"seen_at"`
}
type Alert struct {
	ID string `json:"id"`
	AlertInput
	Service   string `json:"service"`
	Processed bool   `json:"processed"`
}
type Incident struct {
	ID         string    `json:"id"`
	ServiceID  string    `json:"service_id"`
	Service    string    `json:"service"`
	Signature  string    `json:"signature"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity"`
	State      string    `json:"state"`
	Owner      string    `json:"owner"`
	OpenedAt   time.Time `json:"opened_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	AlertCount int       `json:"alert_count"`
	Version    int64     `json:"version"`
}
type Service struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Owner  string `json:"owner"`
	Tier   string `json:"tier"`
	Active int    `json:"active_incidents"`
}
type Event struct {
	ID         int64           `json:"id"`
	IncidentID string          `json:"incident_id"`
	Type       string          `json:"type"`
	Actor      string          `json:"actor"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}
type Rule struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ServiceID *string `json:"service_id"`
	Window    int     `json:"window_seconds"`
	Enabled   bool    `json:"enabled"`
}

func SeverityRank(s string) int {
	return map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}[s]
}
func ValidTransition(from, to string) bool {
	return from != to && ((to == "acknowledged" && from == "open") || (to == "resolved" && from != "resolved") || (to == "open" && from == "resolved"))
}
func (a AlertInput) Validate(now time.Time) error {
	if strings.TrimSpace(a.SourceEventID) == "" || len(a.SourceEventID) > 200 {
		return fmt.Errorf("source_event_id must contain 1–200 characters")
	}
	if strings.TrimSpace(a.Signature) == "" || len(a.Signature) > 120 {
		return fmt.Errorf("signature must contain 1–120 characters")
	}
	if strings.TrimSpace(a.Summary) == "" || len(a.Summary) > 1000 {
		return fmt.Errorf("summary must contain 1–1000 characters")
	}
	if SeverityRank(a.Severity) == 0 {
		return fmt.Errorf("invalid severity")
	}
	if !a.SeenAt.IsZero() && (a.SeenAt.After(now.Add(5*time.Minute)) || a.SeenAt.Before(now.Add(-30*24*time.Hour))) {
		return fmt.Errorf("seen_at must be within the last 30 days and no more than 5 minutes ahead")
	}
	return nil
}
