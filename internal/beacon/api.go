package beacon

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type API struct {
	Store    *Store
	Redis    *redis.Client
	Keys     map[string]string
	Origins  map[string]bool
	EmbedURL string
	Demo     bool
	limiter  sync.Map
}
type orgKey struct{}

func org(r *http.Request) string { return r.Context().Value(orgKey{}).(string) }
func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, r *http.Request, status int, msg string) {
	send(w, status, map[string]any{"code": http.StatusText(status), "message": msg, "request_id": w.Header().Get("X-Request-ID")})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, r, 400, "Invalid JSON: "+err.Error())
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		fail(w, r, 400, "Expected one JSON value")
		return false
	}
	return true
}
func dbError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, 404, "Record not found")
		return
	}
	slog.Error("request failed", "request_id", w.Header().Get("X-Request-ID"), "error", err)
	fail(w, r, 500, "Operation failed")
}
func limit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n < 1 || n > 500 {
		return 100
	}
	return n
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if a.Store.DB.Ping(ctx) != nil {
			fail(w, r, 503, "Database unavailable")
			return
		}
		send(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/alerts", a.ingest)
	mux.HandleFunc("POST /v1/alerts/batch", a.batch)
	mux.HandleFunc("GET /v1/alerts", a.alerts)
	mux.HandleFunc("GET /v1/alerts/{id}", a.alerts)
	mux.HandleFunc("POST /v1/alerts/{id}/replay", a.replay)
	mux.HandleFunc("GET /v1/incidents", a.incidents)
	mux.HandleFunc("POST /v1/incidents", a.createIncident)
	mux.HandleFunc("GET /v1/incidents/{id}", a.detail)
	mux.HandleFunc("PATCH /v1/incidents/{id}", a.patch)
	for route, state := range map[string]string{"ack": "acknowledged", "resolve": "resolved", "reopen": "open"} {
		mux.HandleFunc("POST /v1/incidents/{id}/"+route, func(w http.ResponseWriter, r *http.Request) {
			i, err := a.Store.Transition(r.Context(), org(r), r.PathValue("id"), state, "operator")
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					dbError(w, r, err)
				} else {
					fail(w, r, 409, "Transition not allowed")
				}
				return
			}
			send(w, 200, i)
		})
	}
	mux.HandleFunc("GET /v1/incidents/{id}/timeline", a.timeline)
	mux.HandleFunc("GET /v1/incidents/{id}/alerts", a.alerts)
	mux.HandleFunc("POST /v1/search/incidents", a.search)
	mux.HandleFunc("GET /v1/incidents/{id}/similar", a.search)
	mux.HandleFunc("GET /v1/search/suggestions", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"items": []string{"connection pool exhaustion", "upstream timeout", "payment authorization failure", "memory pressure", "elevated error rate"}})
	})
	mux.HandleFunc("GET /v1/services", a.services)
	mux.HandleFunc("POST /v1/services", a.createService)
	mux.HandleFunc("GET /v1/services/{id}", a.services)
	mux.HandleFunc("GET /v1/services/{id}/status", a.services)
	mux.HandleFunc("GET /v1/rules", a.rules)
	mux.HandleFunc("POST /v1/rules", a.saveRule)
	mux.HandleFunc("PATCH /v1/rules/{id}", a.saveRule)
	mux.HandleFunc("GET /v1/stats", a.stats)
	mux.HandleFunc("GET /v1/events", a.timeline)
	mux.HandleFunc("GET /v1/stream", a.stream)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", uuid.NewString())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !a.Origins[origin] {
				fail(w, r, 403, "Origin not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		tenant := ""
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		for key, id := range a.Keys {
			if subtle.ConstantTimeCompare([]byte(key), []byte(token)) == 1 {
				tenant = id
			}
		}
		// Demo mode is explicitly single tenant, synthetic data only; never trust a tenant header.
		if a.Demo {
			tenant = DemoOrg
		}
		if tenant == "" {
			fail(w, r, 401, "A valid bearer API key is required")
			return
		}
		if id := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/"), "/"); len(id) > 1 && (id[0] == "incidents" || id[0] == "alerts" || id[0] == "services" || id[0] == "rules") && id[1] != "batch" {
			if _, err := uuid.Parse(id[1]); err != nil {
				fail(w, r, 400, "Invalid record ID")
				return
			}
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), orgKey{}, tenant)))
	})
}
func (a *API) ingest(w http.ResponseWriter, r *http.Request) {
	var v AlertInput
	if !decode(w, r, &v) {
		return
	}
	id, dup, err := a.Store.Ingest(r.Context(), org(r), v)
	if err != nil {
		fail(w, r, 400, err.Error())
		return
	}
	status := 202
	if dup {
		status = 200
	}
	send(w, status, map[string]any{"id": id, "duplicate": dup})
}
func (a *API) batch(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Alerts []AlertInput `json:"alerts"`
	}
	if !decode(w, r, &v) {
		return
	}
	if len(v.Alerts) < 1 || len(v.Alerts) > 1000 {
		fail(w, r, 400, "Provide 1–1000 alerts")
		return
	}
	out := []any{}
	for _, item := range v.Alerts {
		id, dup, err := a.Store.Ingest(r.Context(), org(r), item)
		if err != nil {
			out = append(out, map[string]any{"error": err.Error(), "source_event_id": item.SourceEventID})
		} else {
			out = append(out, map[string]any{"id": id, "duplicate": dup})
		}
	}
	send(w, 202, map[string]any{"items": out})
}
func (a *API) incidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	n := limit(r)
	items, err := a.Store.Incidents(r.Context(), org(r), q.Get("state"), q.Get("severity"), q.Get("service_id"), q.Get("q"), q.Get("cursor"), n+1)
	if err != nil {
		dbError(w, r, err)
		return
	}
	cursor := ""
	if len(items) > n {
		items = items[:n]
		cursor = items[n-1].ID
	}
	send(w, 200, map[string]any{"items": items, "next_cursor": cursor})
}
func (a *API) detail(w http.ResponseWriter, r *http.Request) {
	i, err := a.Store.Incident(r.Context(), org(r), r.PathValue("id"))
	if err != nil {
		dbError(w, r, err)
		return
	}
	send(w, 200, i)
}
func (a *API) patch(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Title string `json:"title"`
		Owner string `json:"owner"`
	}
	if !decode(w, r, &v) {
		return
	}
	if len(v.Title) > 1000 || len(v.Owner) > 120 {
		fail(w, r, 400, "Field too long")
		return
	}
	i, err := a.Store.Patch(r.Context(), org(r), r.PathValue("id"), v.Title, v.Owner)
	if err != nil {
		dbError(w, r, err)
		return
	}
	send(w, 200, i)
}
func (a *API) createIncident(w http.ResponseWriter, r *http.Request) {
	var v AlertInput
	if !decode(w, r, &v) {
		return
	}
	v.SourceEventID = "manual-" + uuid.NewString()
	id, _, err := a.Store.Ingest(r.Context(), org(r), v)
	if err != nil {
		fail(w, r, 400, err.Error())
		return
	}
	if err = a.Store.Correlate(r.Context(), org(r), id); err != nil {
		dbError(w, r, err)
		return
	}
	var iid string
	if err = a.Store.DB.QueryRow(r.Context(), `SELECT incident_id FROM incident_alerts WHERE alert_id=$1`, id).Scan(&iid); err != nil {
		dbError(w, r, err)
		return
	}
	i, err := a.Store.Incident(r.Context(), org(r), iid)
	if err != nil {
		dbError(w, r, err)
		return
	}
	send(w, 201, i)
}
func (a *API) alerts(w http.ResponseWriter, r *http.Request) {
	id, incident := r.PathValue("id"), ""
	if strings.Contains(r.URL.Path, "/incidents/") {
		incident = id
		id = ""
		if _, err := a.Store.Incident(r.Context(), org(r), incident); err != nil {
			dbError(w, r, err)
			return
		}
	}
	n := limit(r)
	items, err := a.Store.Alerts(r.Context(), org(r), id, incident, r.URL.Query().Get("cursor"), n+1)
	if err != nil {
		dbError(w, r, err)
		return
	}
	if id != "" {
		if len(items) == 0 {
			fail(w, r, 404, "Alert not found")
		} else {
			send(w, 200, items[0])
		}
		return
	}
	cursor := ""
	if len(items) > n {
		items = items[:n]
		cursor = items[n-1].ID
	}
	send(w, 200, map[string]any{"items": items, "next_cursor": cursor})
}
func (a *API) replay(w http.ResponseWriter, r *http.Request) {
	v, err := a.Store.Alerts(r.Context(), org(r), r.PathValue("id"), "", "", 1)
	if err != nil {
		dbError(w, r, err)
		return
	}
	if len(v) == 0 {
		fail(w, r, 404, "Alert not found")
		return
	}
	if err = a.Store.Correlate(r.Context(), org(r), v[0].ID); err != nil {
		dbError(w, r, err)
		return
	}
	send(w, 200, map[string]any{"id": v[0].ID, "duplicate": true})
}
func (a *API) timeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id != "" {
		if _, err := a.Store.Incident(r.Context(), org(r), id); err != nil {
			dbError(w, r, err)
			return
		}
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	items, err := a.Store.Timeline(r.Context(), org(r), id, cursor)
	if err != nil {
		dbError(w, r, err)
		return
	}
	if len(items) > 0 {
		cursor = items[len(items)-1].ID
	}
	send(w, 200, map[string]any{"items": items, "next_cursor": cursor})
}
func (a *API) services(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.Services(r.Context(), org(r))
	if err != nil {
		dbError(w, r, err)
		return
	}
	if id := r.PathValue("id"); id != "" {
		for _, v := range items {
			if v.ID == id {
				send(w, 200, v)
				return
			}
		}
		fail(w, r, 404, "Service not found")
		return
	}
	send(w, 200, map[string]any{"items": items})
}
func (a *API) createService(w http.ResponseWriter, r *http.Request) {
	var v Service
	if !decode(w, r, &v) {
		return
	}
	if !safeText(v.Name, 100) || len(v.Owner) > 120 {
		fail(w, r, 400, "Valid name and owner required")
		return
	}
	if v.Tier == "" {
		v.Tier = "tier-2"
	}
	v.ID = uuid.NewString()
	_, err := a.Store.DB.Exec(r.Context(), `INSERT INTO services(id,org_id,name,owner,tier) VALUES($1,$2,$3,$4,$5)`, v.ID, org(r), v.Name, v.Owner, v.Tier)
	if err != nil {
		fail(w, r, 409, "Service could not be created")
		return
	}
	send(w, 201, v)
}
func (a *API) rules(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.Rules(r.Context(), org(r))
	if err != nil {
		dbError(w, r, err)
		return
	}
	send(w, 200, map[string]any{"items": items})
}
func (a *API) saveRule(w http.ResponseWriter, r *http.Request) {
	var v Rule
	if !decode(w, r, &v) {
		return
	}
	if !safeText(v.Name, 100) || v.Window < 60 || v.Window > 86400 {
		fail(w, r, 400, "A name and window_seconds between 60 and 86400 are required")
		return
	}
	if v.ServiceID != nil {
		var ok bool
		err := a.Store.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM services WHERE id::text=$1 AND org_id=$2)`, *v.ServiceID, org(r)).Scan(&ok)
		if err != nil || !ok {
			fail(w, r, 400, "Unknown service")
			return
		}
	}
	var err error
	id := r.PathValue("id")
	if id == "" {
		v.ID = uuid.NewString()
		_, err = a.Store.DB.Exec(r.Context(), `INSERT INTO rules(id,org_id,name,service_id,window_seconds,enabled) VALUES($1,$2,$3,$4,$5,$6)`, v.ID, org(r), v.Name, v.ServiceID, v.Window, v.Enabled)
	} else {
		v.ID = id
		result, e := a.Store.DB.Exec(r.Context(), `UPDATE rules SET name=$3,service_id=$4,window_seconds=$5,enabled=$6 WHERE id=$1 AND org_id=$2`, id, org(r), v.Name, v.ServiceID, v.Window, v.Enabled)
		err = e
		if err == nil && result.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
	}
	if err != nil {
		dbError(w, r, err)
		return
	}
	send(w, 200, v)
}
func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	var total, pending, active, resolved, indexed int64
	err := a.Store.DB.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM alerts WHERE org_id=$1),(SELECT count(*) FROM alerts WHERE org_id=$1 AND NOT processed),(SELECT count(*) FROM incidents WHERE org_id=$1 AND state<>'resolved'),(SELECT count(*) FROM incidents WHERE org_id=$1 AND state='resolved'),(SELECT count(*) FROM incident_embeddings e JOIN incidents i ON i.id=e.incident_id WHERE i.org_id=$1)`, org(r)).Scan(&total, &pending, &active, &resolved, &indexed)
	if err != nil {
		dbError(w, r, err)
		return
	}
	rows, err := a.Store.DB.Query(r.Context(), `SELECT date_trunc('minute',seen_at) AS bucket,count(*) FROM alerts WHERE org_id=$1 AND seen_at>now()-interval '1 hour' GROUP BY bucket ORDER BY bucket`, org(r))
	if err != nil {
		dbError(w, r, err)
		return
	}
	defer rows.Close()
	buckets := []any{}
	for rows.Next() {
		var t time.Time
		var c int
		if err = rows.Scan(&t, &c); err != nil {
			dbError(w, r, err)
			return
		}
		buckets = append(buckets, map[string]any{"at": t, "count": c})
	}
	send(w, 200, map[string]any{"alerts": total, "pending": pending, "active": active, "resolved": resolved, "indexed": indexed, "buckets": buckets, "mode": "live"})
}
func (a *API) search(w http.ResponseWriter, r *http.Request) {
	query := ""
	exclude := r.PathValue("id")
	if exclude != "" {
		i, err := a.Store.Incident(r.Context(), org(r), exclude)
		if err != nil {
			dbError(w, r, err)
			return
		}
		query = i.Title + " " + i.Signature
	} else {
		var v struct {
			Query string `json:"query"`
		}
		if !decode(w, r, &v) {
			return
		}
		query = v.Query
	}
	if !safeText(query, 1000) {
		fail(w, r, 400, "query must contain 1–1000 characters")
		return
	}
	b, _ := json.Marshal(map[string]string{"text": query})
	req, err := http.NewRequestWithContext(r.Context(), "POST", a.EmbedURL+"/embed", bytes.NewReader(b))
	if err != nil {
		dbError(w, r, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fail(w, r, 503, "Embedding worker is warming up; retry shortly")
		return
	}
	defer res.Body.Close()
	var v struct {
		Vector []float64 `json:"vector"`
		Model  string    `json:"model"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&v) != nil || len(v.Vector) != 384 {
		fail(w, r, 503, "Embedding worker unavailable")
		return
	}
	vec, _ := json.Marshal(v.Vector)
	// Exact tenant-filtered ranking avoids ANN tenant-filter recall surprises on a small portfolio dataset.
	rows, err := a.Store.DB.Query(r.Context(), `SELECT `+incidentColumns+`,1-(e.embedding <=> $2::vector) AS score FROM incident_embeddings e JOIN incidents i ON i.id=e.incident_id JOIN services s ON s.id=i.service_id WHERE i.org_id=$1 AND e.model_version=$3 AND i.id::text<>$4 ORDER BY e.embedding <=> $2::vector LIMIT 8`, org(r), string(vec), v.Model, exclude)
	if err != nil {
		dbError(w, r, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var i Incident
		var score float64
		err = rows.Scan(&i.ID, &i.ServiceID, &i.Service, &i.Signature, &i.Title, &i.Severity, &i.State, &i.Owner, &i.OpenedAt, &i.UpdatedAt, &i.AlertCount, &i.Version, &score)
		if err != nil {
			dbError(w, r, err)
			return
		}
		items = append(items, map[string]any{"incident": i, "score": score, "reason": "Shared failure language · " + i.Signature})
	}
	send(w, 200, map[string]any{"items": items, "model": v.Model})
}
func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	// Same-origin demo only; authenticated API clients use Authorization during handshake.
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { o := r.Header.Get("Origin"); return o == "" || a.Origins[o] }}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sub := a.Redis.Subscribe(ctx, "beacon:"+org(r))
	defer sub.Close()
	if _, err = sub.Receive(ctx); err != nil {
		return
	}
	queue := make(chan []byte, 256)
	overflow := make(chan struct{})
	go func() {
		defer close(overflow)
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-sub.Channel():
				if !ok {
					return
				}
				select {
				case queue <- []byte(m.Payload):
				default:
					return
				}
			}
		}
	}()
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	// Snapshot is authoritative. Client reconnects, reloads state, then resumes the durable event cursor.
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	write := func(v any) error {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}
	if err = write(map[string]any{"event_type": "resync", "cursor": cursor}); err != nil {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-overflow:
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "resync required"), time.Now().Add(time.Second))
			return
		case b := <-queue:
			var v any
			if json.Unmarshal(b, &v) == nil {
				if write(v) != nil {
					return
				}
			}
		case <-ticker.C:
			if conn.WriteControl(websocket.PingMessage, []byte(fmt.Sprint(time.Now().Unix())), time.Now().Add(time.Second)) != nil {
				return
			}
		}
	}
}
