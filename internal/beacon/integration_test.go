package beacon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	b, err := os.ReadFile("../../db/migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(b)); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	if err = s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
func testAlert(key string) AlertInput {
	return AlertInput{SourceEventID: key, ServiceID: "b0000000-0000-4000-8000-000000000010", Signature: "pool", Severity: "medium", Summary: "Database pool exhausted", SeenAt: time.Now().UTC()}
}
func TestConcurrentDedupeAndCorrelation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan string, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := s.Ingest(ctx, DemoOrg, testAlert("same-source"))
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Error("duplicate produced a different alert")
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := s.Correlate(ctx, DemoOrg, id); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	var alerts, incidents, links, outbox int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM alerts),(SELECT count(*) FROM incidents),(SELECT count(*) FROM incident_alerts),(SELECT count(*) FROM outbox_events)`).Scan(&alerts, &incidents, &links, &outbox); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 || incidents != 1 || links != 1 || outbox != 2 {
		t.Fatalf("counts: %d %d %d %d", alerts, incidents, links, outbox)
	}
}
func TestGroupingSeverityWindowsAndLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	for n := 0; n < 10; n++ {
		a := testAlert(fmt.Sprint(n))
		a.SeenAt = base.Add(time.Duration(n) * time.Second)
		if n == 5 {
			a.Severity = "critical"
		}
		id, _, err := s.Ingest(ctx, DemoOrg, a)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Correlate(ctx, DemoOrg, id); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.Incidents(ctx, DemoOrg, "", "", "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AlertCount != 10 || list[0].Severity != "critical" {
		t.Fatalf("unexpected grouping: %+v", list)
	}
	i := list[0]
	for _, state := range []string{"acknowledged", "resolved", "open"} {
		if _, err = s.Transition(ctx, DemoOrg, i.ID, state, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Transition(ctx, DemoOrg, i.ID, "resolved", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(ctx, DemoOrg, i.ID, "acknowledged", "test"); err == nil {
		t.Fatal("illegal transition accepted")
	}
	a := testAlert("later")
	id, _, _ := s.Ingest(ctx, DemoOrg, a)
	if err = s.Correlate(ctx, DemoOrg, id); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Incidents(ctx, DemoOrg, "", "", "", "", "", 100)
	if len(list) != 2 {
		t.Fatal("resolved incident was reused")
	}
}
func TestTenantAuthorizationAndPagination(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	other := uuid.NewString()
	_, err := s.DB.Exec(ctx, `INSERT INTO organizations(id,slug) VALUES($1,'other')`, other)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		a := testAlert(fmt.Sprint(n))
		a.Signature = fmt.Sprint(n)
		id, _, _ := s.Ingest(ctx, DemoOrg, a)
		if err = s.Correlate(ctx, DemoOrg, id); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.Incidents(ctx, DemoOrg, "", "", "", "", "", 100)
	api := (&API{Store: s, Keys: map[string]string{"first": DemoOrg, "other": other}, Origins: map[string]bool{"http://allowed.test": true}}).Handler()
	check := func(path, key string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: got %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	check("/v1/incidents", "", 401)
	check("/v1/incidents/"+list[0].ID, "other", 404)
	check("/v1/incidents/"+list[0].ID+"/timeline", "other", 404)
	check("/v1/incidents/not-a-uuid", "first", 400)
	w := check("/v1/incidents", "other", 200)
	if strings.Contains(w.Body.String(), list[0].Title) {
		t.Fatal("tenant leak")
	}
	seen := map[string]bool{}
	cursor := ""
	for n := 0; n < 3; n++ {
		w = check("/v1/incidents?limit=1&cursor="+cursor, "first", 200)
		var page struct {
			Items []Incident `json:"items"`
			Next  string     `json:"next_cursor"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || seen[page.Items[0].ID] {
			t.Fatal("unstable pagination")
		}
		seen[page.Items[0].ID] = true
		cursor = page.Next
	}
	if cursor != "" {
		t.Fatal("expected final cursor")
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	r.Header.Set("Origin", "http://evil.test")
	w = httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("bad origin accepted")
	}
	if _, _, err = s.Ingest(ctx, other, testAlert("foreign-service")); err == nil {
		t.Fatal("foreign service accepted")
	}
}
func TestConcurrentDifferentAlertsShareIncident(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id, _, err := s.Ingest(ctx, DemoOrg, testAlert(fmt.Sprint(n)))
			if err == nil {
				err = s.Correlate(ctx, DemoOrg, id)
			}
			if err != nil {
				t.Error(err)
			}
		}(n)
	}
	wg.Wait()
	list, err := s.Incidents(ctx, DemoOrg, "", "", "", "", "", 100)
	if err != nil || len(list) != 1 || list[0].AlertCount != 20 {
		t.Fatalf("concurrent grouping: %+v %v", list, err)
	}
}

func TestVectorSearchTenantIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	other := uuid.NewString()
	if _, err := s.DB.Exec(ctx, `INSERT INTO organizations(id,slug) VALUES($1,'vector-other')`, other); err != nil {
		t.Fatal(err)
	}
	otherService := uuid.NewString()
	if _, err := s.DB.Exec(ctx, `INSERT INTO services(id,org_id,name) VALUES($1,$2,'private')`, otherService, other); err != nil {
		t.Fatal(err)
	}
	vector := make([]float64, 384)
	vector[0] = 1
	b, _ := json.Marshal(vector)
	for _, tenant := range []string{DemoOrg, other} {
		a := testAlert("vector-alert")
		if tenant == other {
			a.ServiceID = otherService
			a.Summary = "SECRET foreign incident"
		}
		id, _, err := s.Ingest(ctx, tenant, a)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Correlate(ctx, tenant, id); err != nil {
			t.Fatal(err)
		}
		items, _ := s.Incidents(ctx, tenant, "", "", "", "", "", 100)
		if _, err = s.DB.Exec(ctx, `INSERT INTO incident_embeddings(incident_id,embedding,model_version,incident_version) VALUES($1,$2::vector,'test-model',1)`, items[0].ID, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	encoder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"vector": vector, "model": "test-model"})
	}))
	defer encoder.Close()
	api := (&API{Store: s, Keys: map[string]string{"first": DemoOrg}, EmbedURL: encoder.URL}).Handler()
	req := httptest.NewRequest("POST", "/v1/search/incidents", strings.NewReader(`{"query":"pool"}`))
	req.Header.Set("Authorization", "Bearer first")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal("cross tenant vector leak")
	}
	var result struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatalf("wrong results %s", w.Body.String())
	}
}

func TestCorrelationWindowBoundary(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()
	for n, ago := range []time.Duration{time.Hour, time.Hour - 899*time.Second, time.Hour - 1800*time.Second} {
		a := testAlert(fmt.Sprint(n))
		a.SeenAt = now.Add(-ago)
		id, _, err := s.Ingest(ctx, DemoOrg, a)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Correlate(ctx, DemoOrg, id); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Incidents(ctx, DemoOrg, "", "", "", "", "", 100)
	if err != nil || len(items) != 2 {
		t.Fatalf("expected two window groups; got %+v %v", items, err)
	}
}
