package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

func TestEnrollmentProbesAndDuplicateNames(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := New(db, "test-agent-secret")
	server := httptest.NewServer(http.HandlerFunc(hub.ServeAgent))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello := func(name, token, instance string, probe bool) (*websocket.Conn, map[string]string) {
		t.Helper()
		ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"type": "hello", "machine": name, "token": token, "instance_id": instance, "probe": probe})
		if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
		_, raw, err = ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var reply map[string]string
		if err := json.Unmarshal(raw, &reply); err != nil {
			t.Fatal(err)
		}
		return ws, reply
	}
	for _, tc := range []struct{ name, token, code string }{{"desk", "bad", "authentication_failed"}, {"bad name", "test-agent-secret", "invalid_machine"}} {
		ws, reply := hello(tc.name, tc.token, "one", false)
		ws.CloseNow()
		if reply["code"] != tc.code {
			t.Fatalf("wrong rejection code: %s", reply["code"])
		}
	}
	probe, reply := hello("desk", "test-agent-secret", "one", true)
	probe.CloseNow()
	if reply["type"] != "welcome" || hub.Online("desk") {
		t.Fatal("probe registered a live agent")
	}
	machines, _ := db.ListMachines(ctx, db.UserID)
	if len(machines) != 0 {
		t.Fatal("probe created machine state")
	}
	first, reply := hello("desk", "test-agent-secret", "one", false)
	defer first.CloseNow()
	if reply["type"] != "welcome" || !hub.Online("desk") {
		t.Fatal("agent did not connect")
	}
	original := hub.get("desk")
	second, reply := hello("desk", "test-agent-secret", "two", false)
	second.CloseNow()
	if reply["code"] != "machine_in_use" || hub.get("desk") != original {
		t.Fatal("duplicate replaced active connection")
	}
	self, reply := hello("desk", "test-agent-secret", "one", true)
	self.CloseNow()
	if reply["type"] != "welcome" || hub.get("desk") != original {
		t.Fatal("self probe interrupted active connection")
	}
	other, reply := hello("desk", "test-agent-secret", "two", true)
	other.CloseNow()
	if reply["code"] != "machine_in_use" {
		t.Fatal("probe missed duplicate machine")
	}
}
