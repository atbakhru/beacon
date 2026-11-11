package beacon

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveWebSocketResync(t *testing.T) {
	base := os.Getenv("BEACON_TEST_URL")
	if base == "" {
		t.Skip("BEACON_TEST_URL not set")
	}
	ws := strings.Replace(base, "http", "ws", 1) + "/v1/stream"
	conn, _, err := websocket.DefaultDialer.Dial(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var e map[string]any
	if err = conn.ReadJSON(&e); err != nil || e["event_type"] != "resync" {
		t.Fatalf("missing resync %v %v", e, err)
	}
	a := testAlert(uuid.NewString())
	b, _ := json.Marshal(a)
	resp, err := http.Post(base+"/v1/alerts", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("ingest %d", resp.StatusCode)
	}
	for {
		if err = conn.ReadJSON(&e); err != nil {
			t.Fatal(err)
		}
		if e["cursor"] != nil {
			break
		}
	}
	_ = conn.Close()
	conn2, _, err := websocket.DefaultDialer.Dial(ws+"?cursor=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	_ = conn2.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err = conn2.ReadJSON(&e); err != nil || e["event_type"] != "resync" {
		t.Fatalf("reconnect resync %v %v", e, err)
	}
	resp, err = http.Get(base + "/v1/events?cursor=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page struct {
		Items []Event `json:"items"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&page); err != nil || len(page.Items) == 0 {
		t.Fatal("durable recovery returned no events")
	}
}
