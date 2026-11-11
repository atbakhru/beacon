package beacon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ DB *pgxpool.Pool }

func (s *Store) Seed(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO organizations(id,slug) VALUES($1,'acme-demo') ON CONFLICT DO NOTHING`, DemoOrg)
	if err != nil {
		return err
	}
	for i, n := range []string{"checkout-api", "payment-service", "auth-service", "notification-worker", "inventory-api", "api-gateway"} {
		_, err = s.DB.Exec(ctx, `INSERT INTO services(id,org_id,name,owner,tier) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, fmt.Sprintf("b0000000-0000-4000-8000-%012d", i+10), DemoOrg, n, []string{"Commerce", "Payments", "Identity", "Platform", "Commerce", "Platform"}[i], "tier-1")
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Ingest(ctx context.Context, org string, a AlertInput) (string, bool, error) {
	if err := a.Validate(time.Now()); err != nil {
		return "", false, err
	}
	if _, err := uuid.Parse(a.ServiceID); err != nil {
		return "", false, fmt.Errorf("invalid service_id")
	}
	if a.SeenAt.IsZero() {
		a.SeenAt = time.Now().UTC()
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM services WHERE id=$1 AND org_id=$2)`, a.ServiceID, org).Scan(&exists)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, fmt.Errorf("unknown service")
	}
	id := uuid.NewString()
	raw, _ := json.Marshal(a)
	result, err := tx.Exec(ctx, `INSERT INTO alerts(id,org_id,source_event_id,service_id,signature,severity,summary,seen_at,raw_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(org_id,source_event_id) DO NOTHING`, id, org, a.SourceEventID, a.ServiceID, a.Signature, a.Severity, a.Summary, a.SeenAt, raw)
	if err != nil {
		return "", false, err
	}
	if result.RowsAffected() == 0 {
		err = tx.QueryRow(ctx, `SELECT id FROM alerts WHERE org_id=$1 AND source_event_id=$2`, org, a.SourceEventID).Scan(&id)
		return id, true, err
	}
	payload, _ := json.Marshal(map[string]any{"event_id": uuid.NewString(), "event_type": "alert.received", "schema_version": 1, "org_id": org, "alert_id": id, "occurred_at": time.Now().UTC()})
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(topic,key,payload) VALUES('alerts.raw.v1',$1,$2)`, org+":"+a.ServiceID, payload)
	if err != nil {
		return "", false, err
	}
	return id, false, tx.Commit(ctx)
}
func writeEvent(ctx context.Context, tx pgx.Tx, org, id, kind, actor string, payload any) error {
	b, _ := json.Marshal(payload)
	var cursor int64
	if err := tx.QueryRow(ctx, `INSERT INTO incident_events(org_id,incident_id,type,actor,payload) VALUES($1,$2,$3,$4,$5) RETURNING id`, org, id, kind, actor, b).Scan(&cursor); err != nil {
		return err
	}
	event, _ := json.Marshal(map[string]any{"event_id": uuid.NewString(), "schema_version": 1, "org_id": org, "incident_id": id, "event_type": kind, "cursor": cursor, "occurred_at": time.Now().UTC()})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(topic,key,payload) VALUES('incidents.events.v1',$1,$2)`, org+":"+id, event)
	return err
}
func (s *Store) Correlate(ctx context.Context, org, id string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var a AlertInput
	var done bool
	err = tx.QueryRow(ctx, `SELECT service_id,signature,severity,summary,seen_at,processed FROM alerts WHERE id=$1 AND org_id=$2 FOR UPDATE`, id, org).Scan(&a.ServiceID, &a.Signature, &a.Severity, &a.Summary, &a.SeenAt, &done)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	// Serializes a correlation key even when no incident exists yet.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, org+":"+a.ServiceID+":"+a.Signature); err != nil {
		return err
	}
	window := 900
	err = tx.QueryRow(ctx, `SELECT window_seconds FROM rules WHERE org_id=$1 AND enabled AND (service_id=$2 OR service_id IS NULL) ORDER BY service_id NULLS LAST,id LIMIT 1`, org, a.ServiceID).Scan(&window)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var incidentID, severity string
	kind := "incident.updated"
	err = tx.QueryRow(ctx, `SELECT id,severity FROM incidents WHERE org_id=$1 AND service_id=$2 AND signature=$3 AND state<>'resolved' AND updated_at >= $4 AND opened_at <= $5 ORDER BY updated_at DESC LIMIT 1 FOR UPDATE`, org, a.ServiceID, a.Signature, a.SeenAt.Add(-time.Duration(window)*time.Second), a.SeenAt.Add(time.Duration(window)*time.Second)).Scan(&incidentID, &severity)
	if errors.Is(err, pgx.ErrNoRows) {
		incidentID = uuid.NewString()
		kind = "incident.created"
		_, err = tx.Exec(ctx, `INSERT INTO incidents(id,org_id,service_id,signature,title,severity,opened_at,updated_at,alert_count) VALUES($1,$2,$3,$4,$5,$6,$7,$7,1)`, incidentID, org, a.ServiceID, a.Signature, a.Summary, a.Severity, a.SeenAt)
	} else if err == nil {
		if SeverityRank(a.Severity) > SeverityRank(severity) {
			severity = a.Severity
		}
		_, err = tx.Exec(ctx, `UPDATE incidents SET alert_count=alert_count+1,severity=$2,updated_at=GREATEST(updated_at,$3),opened_at=LEAST(opened_at,$3),version=version+1 WHERE id=$1`, incidentID, severity, a.SeenAt)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO incident_alerts(incident_id,alert_id) VALUES($1,$2)`, incidentID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE alerts SET processed=true WHERE id=$1`, id); err != nil {
		return err
	}
	if err = writeEvent(ctx, tx, org, incidentID, kind, "correlator", map[string]string{"alert_id": id, "summary": a.Summary}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const incidentColumns = `i.id,i.service_id,s.name,i.signature,i.title,i.severity,i.state,i.owner,i.opened_at,i.updated_at,i.alert_count,i.version`

func scanIncident(row pgx.Row) (i Incident, err error) {
	err = row.Scan(&i.ID, &i.ServiceID, &i.Service, &i.Signature, &i.Title, &i.Severity, &i.State, &i.Owner, &i.OpenedAt, &i.UpdatedAt, &i.AlertCount, &i.Version)
	return
}
func (s *Store) Incident(ctx context.Context, org, id string) (Incident, error) {
	return scanIncident(s.DB.QueryRow(ctx, `SELECT `+incidentColumns+` FROM incidents i JOIN services s ON s.id=i.service_id WHERE i.org_id=$1 AND i.id=$2`, org, id))
}
func (s *Store) Incidents(ctx context.Context, org, state, severity, service, q, cursor string, limit int) ([]Incident, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+incidentColumns+` FROM incidents i JOIN services s ON s.id=i.service_id WHERE i.org_id=$1 AND ($2='' OR i.state=$2) AND ($3='' OR i.severity=$3) AND ($4='' OR i.service_id::text=$4) AND ($5='' OR i.title ILIKE '%'||$5||'%' OR s.name ILIKE '%'||$5||'%') AND ($6='' OR i.id::text>$6) ORDER BY i.id LIMIT $7`, org, state, severity, service, q, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		i, e := scanIncident(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Store) Transition(ctx context.Context, org, id, state, actor string) (Incident, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback(ctx)
	var from string
	err = tx.QueryRow(ctx, `SELECT state FROM incidents WHERE id=$1 AND org_id=$2 FOR UPDATE`, id, org).Scan(&from)
	if err != nil {
		return Incident{}, err
	}
	if from == state {
		return s.Incident(ctx, org, id)
	}
	if !ValidTransition(from, state) {
		return Incident{}, fmt.Errorf("invalid transition from %s to %s", from, state)
	}
	_, err = tx.Exec(ctx, `UPDATE incidents SET state=$3,updated_at=now(),closed_at=CASE WHEN $3='resolved' THEN now() ELSE NULL END,version=version+1 WHERE id=$1 AND org_id=$2`, id, org, state)
	if err != nil {
		return Incident{}, err
	}
	if err = writeEvent(ctx, tx, org, id, "incident."+state, actor, map[string]string{"from": from, "to": state}); err != nil {
		return Incident{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Incident{}, err
	}
	return s.Incident(ctx, org, id)
}
func (s *Store) Patch(ctx context.Context, org, id, title, owner string) (Incident, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback(ctx)
	r, err := tx.Exec(ctx, `UPDATE incidents SET title=COALESCE(NULLIF($3,''),title),owner=$4,updated_at=now(),version=version+1 WHERE id=$1 AND org_id=$2`, id, org, title, owner)
	if err != nil {
		return Incident{}, err
	}
	if r.RowsAffected() == 0 {
		return Incident{}, pgx.ErrNoRows
	}
	if err = writeEvent(ctx, tx, org, id, "incident.updated", "operator", map[string]string{"owner": owner, "title": title}); err != nil {
		return Incident{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Incident{}, err
	}
	return s.Incident(ctx, org, id)
}
func (s *Store) Timeline(ctx context.Context, org, id string, cursor int64) ([]Event, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,incident_id,type,actor,payload,created_at FROM incident_events WHERE org_id=$1 AND ($2='' OR incident_id::text=$2) AND id>$3 ORDER BY id LIMIT 500`, org, id, cursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.ID, &e.IncidentID, &e.Type, &e.Actor, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Services(ctx context.Context, org string) ([]Service, error) {
	rows, err := s.DB.Query(ctx, `SELECT s.id,s.name,s.owner,s.tier,count(i.id) FILTER(WHERE i.state<>'resolved') FROM services s LEFT JOIN incidents i ON s.id=i.service_id WHERE s.org_id=$1 GROUP BY s.id ORDER BY s.name`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Service{}
	for rows.Next() {
		var v Service
		if err = rows.Scan(&v.ID, &v.Name, &v.Owner, &v.Tier, &v.Active); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Alerts(ctx context.Context, org, id, incident, cursor string, limit int) ([]Alert, error) {
	rows, err := s.DB.Query(ctx, `SELECT a.id,a.source_event_id,a.service_id,s.name,a.signature,a.severity,a.summary,a.seen_at,a.processed FROM alerts a JOIN services s ON s.id=a.service_id WHERE a.org_id=$1 AND ($2='' OR a.id::text=$2) AND ($3='' OR a.id IN (SELECT alert_id FROM incident_alerts WHERE incident_id::text=$3)) AND ($4='' OR a.id::text>$4) ORDER BY a.id LIMIT $5`, org, id, incident, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		if err = rows.Scan(&a.ID, &a.SourceEventID, &a.ServiceID, &a.Service, &a.Signature, &a.Severity, &a.Summary, &a.SeenAt, &a.Processed); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Rules(ctx context.Context, org string) ([]Rule, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,service_id,window_seconds,enabled FROM rules WHERE org_id=$1 ORDER BY id`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err = rows.Scan(&r.ID, &r.Name, &r.ServiceID, &r.Window, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func safeText(v string, max int) bool { return len(strings.TrimSpace(v)) > 0 && len(v) <= max }
