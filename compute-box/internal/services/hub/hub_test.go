package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/coder/websocket"
)

func TestMachineTokensProbesAndDuplicates(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := New(db)
	server := httptest.NewServer(http.HandlerFunc(hub.ServeAgent))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	owner := db.DefaultUserID()
	token, err := db.IssueMachineToken(ctx, owner, "desk")
	if err != nil {
		t.Fatal(err)
	}
	desk := Machine{User: owner, Name: "desk"}

	hello := func(token, instance string, probe bool) (*websocket.Conn, map[string]string) {
		t.Helper()
		ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		// "machine" is what older agents send; the token alone decides who connects.
		raw, _ := json.Marshal(map[string]any{"type": "hello", "machine": "someone-else", "token": token, "instance_id": instance, "probe": probe})
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

	for _, bad := range []string{"", "bad", token + "x"} {
		ws, reply := hello(bad, "one", false)
		ws.CloseNow()
		if reply["code"] != "authentication_failed" {
			t.Fatalf("token %q: wrong rejection %v", bad, reply)
		}
	}
	probe, reply := hello(token, "one", true)
	probe.CloseNow()
	if reply["type"] != "welcome" || reply["machine"] != "desk" || hub.Online(desk) {
		t.Fatalf("probe: %v, online=%v", reply, hub.Online(desk))
	}
	machines, _ := db.ListMachines(ctx, owner)
	if len(machines) != 1 || machines[0].LastSeen != "" {
		t.Fatalf("probe recorded a connection: %+v", machines)
	}

	first, reply := hello(token, "one", false)
	defer first.CloseNow()
	if reply["type"] != "welcome" || reply["machine"] != "desk" || !hub.Online(desk) {
		t.Fatalf("agent did not connect: %v", reply)
	}
	original := hub.get(desk)
	second, reply := hello(token, "two", false)
	second.CloseNow()
	if reply["code"] != "machine_in_use" || hub.get(desk) != original {
		t.Fatal("duplicate replaced active connection")
	}
	self, reply := hello(token, "one", true)
	self.CloseNow()
	if reply["type"] != "welcome" || hub.get(desk) != original {
		t.Fatal("self probe interrupted active connection")
	}
	other, reply := hello(token, "two", true)
	other.CloseNow()
	if reply["code"] != "machine_in_use" {
		t.Fatal("probe missed duplicate machine")
	}

	// Another user's machine with the same name is a different machine.
	stranger, _ := db.EnsureUser(ctx, "stranger")
	strangerToken, _ := db.IssueMachineToken(ctx, stranger, "desk")
	theirs, reply := hello(strangerToken, "three", false)
	defer theirs.CloseNow()
	if reply["type"] != "welcome" || !hub.Online(Machine{User: stranger, Name: "desk"}) || hub.get(desk) != original {
		t.Fatalf("same name for another user: %v", reply)
	}

	// A new token cuts off the old one: the live agent is dropped and can't come back.
	if _, err := db.IssueMachineToken(ctx, owner, "desk"); err != nil {
		t.Fatal(err)
	}
	hub.Disconnect(desk)
	deadline := time.Now().Add(3 * time.Second)
	for hub.Online(desk) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.Online(desk) {
		t.Fatal("disconnect left the agent online")
	}
	again, reply := hello(token, "one", false)
	again.CloseNow()
	if reply["code"] != "authentication_failed" {
		t.Fatalf("old token still works: %v", reply)
	}
}
