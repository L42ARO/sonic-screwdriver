package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// ErrOffline means the machine has no live connection.
var ErrOffline = errors.New("machine is not connected")

// ErrDisconnected means the connection dropped while an action was in flight.
var ErrDisconnected = errors.New("machine disconnected")

// Machine identifies a connected machine: names are unique per owner, not globally.
type Machine struct {
	User int64
	Name string
}

type Hub struct {
	store *store.Store

	mu      sync.Mutex
	conns   map[Machine]*conn
	changed chan struct{} // closed and replaced whenever a machine connects or leaves
}

type conn struct {
	instanceID string
	machine    Machine
	screen     Screen
	ws         *websocket.Conn
	writeMu    sync.Mutex
	nextID     atomic.Int64

	mu      sync.Mutex
	pending map[string]chan Result
	closed  chan struct{}
}

func New(s *store.Store) *Hub {
	return &Hub{store: s, conns: map[Machine]*conn{}, changed: make(chan struct{})}
}

// ServeAgent upgrades a request to a WebSocket and serves one local-agent until it leaves.
func (h *Hub) ServeAgent(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ws.SetReadLimit(32 << 20) // screenshots are large

	ctx := r.Context()
	helloCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, data, err := ws.Read(helloCtx)
	cancel()
	var hi hello
	if err != nil || json.Unmarshal(data, &hi) != nil || hi.Type != "hello" {
		ws.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	// The machine token identifies both the owner and the machine; the hello's own
	// "machine" field (sent by older agents) is ignored.
	userID, name, ok, err := h.store.MachineByToken(ctx, hi.Token)
	if err != nil || !ok {
		if err != nil {
			log.Printf("hub: looking up machine token: %v", err)
		}
		writeJSON(ctx, ws, nil, map[string]string{"type": "error", "code": "authentication_failed", "message": "Machine token rejected. Add the machine in the dashboard, or regenerate its token, and paste the new token."})
		ws.Close(websocket.StatusPolicyViolation, "bad token")
		return
	}
	m := Machine{User: userID, Name: name}
	if hi.Probe {
		if existing := h.get(m); existing != nil && (hi.InstanceID == "" || existing.instanceID != hi.InstanceID) {
			writeJSON(ctx, ws, nil, map[string]string{"type": "error", "code": "machine_in_use", "message": "This machine is already connected from another agent."})
		} else {
			writeJSON(ctx, ws, nil, map[string]string{"type": "welcome", "machine": name})
		}
		ws.Close(websocket.StatusNormalClosure, "connection test complete")
		return
	}
	c := &conn{machine: m, instanceID: hi.InstanceID, screen: hi.Screen, ws: ws, pending: map[string]chan Result{}, closed: make(chan struct{})}
	if !h.register(c) {
		writeJSON(ctx, ws, nil, map[string]string{"type": "error", "code": "machine_in_use", "message": "This machine is already connected from another agent."})
		ws.Close(websocket.StatusPolicyViolation, "machine in use")
		return
	}
	defer h.unregister(c)
	screen, _ := json.Marshal(hi.Screen)
	if err := h.store.TouchMachine(ctx, userID, name, string(screen)); err != nil {
		log.Printf("hub: recording machine %s: %v", name, err)
	}

	log.Printf("hub: %s (user %d) connected (agent %s, screen %dx%d)", name, userID, hi.Version, hi.Screen.Width, hi.Screen.Height)
	if err := writeJSON(ctx, ws, &c.writeMu, map[string]string{"type": "welcome", "machine": name}); err != nil {
		return
	}

	go c.pingLoop(ctx)
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			log.Printf("hub: %s (user %d) disconnected: %v", c.machine.Name, c.machine.User, err)
			ws.CloseNow()
			return
		}
		var msg inbound
		if json.Unmarshal(data, &msg) != nil || msg.Type != "result" {
			continue
		}
		var res Result
		if err := json.Unmarshal(msg.Result, &res); err != nil {
			res = Result{OK: false, Error: "unreadable result: " + err.Error()}
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	}
}

// Do sends one action to a machine and waits for its result.
func (h *Hub) Do(ctx context.Context, machine Machine, action json.RawMessage) (Result, error) {
	c := h.get(machine)
	if c == nil {
		return Result{}, ErrOffline
	}
	id := fmt.Sprintf("a-%d", c.nextID.Add(1))
	ch := make(chan Result, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := writeJSON(ctx, c.ws, &c.writeMu, actionMsg{Type: "action", ID: id, Action: action}); err != nil {
		return Result{}, ErrDisconnected
	}
	select {
	case res := <-ch:
		return res, nil
	case <-c.closed:
		return Result{}, ErrDisconnected
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// Tell sends a fire-and-forget message (job_start, status, job_end) for the overlay.
func (h *Hub) Tell(machine Machine, msg any) {
	c := h.get(machine)
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writeJSON(ctx, c.ws, &c.writeMu, msg)
}

// Online reports whether a machine is connected.
func (h *Hub) Online(machine Machine) bool { return h.get(machine) != nil }

// ScreenOf returns a connected machine's screenshot geometry.
func (h *Hub) ScreenOf(machine Machine) (Screen, bool) {
	if c := h.get(machine); c != nil {
		return c.screen, true
	}
	return Screen{}, false
}

// WaitOnline blocks until the machine is connected or ctx ends.
func (h *Hub) WaitOnline(ctx context.Context, machine Machine) bool {
	for {
		h.mu.Lock()
		_, ok := h.conns[machine]
		changed := h.changed
		h.mu.Unlock()
		if ok {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

// Disconnect closes a machine's live connection, e.g. after its token was replaced or the
// machine was removed. The agent's reconnect then fails until it has a valid token.
func (h *Hub) Disconnect(machine Machine) {
	if c := h.get(machine); c != nil {
		c.ws.CloseNow() // immediate: a close handshake would wait on an unresponsive agent
	}
}

func (h *Hub) get(machine Machine) *conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[machine]
}

func (h *Hub) register(c *conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[c.machine] != nil {
		return false
	}
	h.conns[c.machine] = c
	h.signal()
	return true
}

func (h *Hub) unregister(c *conn) {
	close(c.closed)
	h.mu.Lock()
	if h.conns[c.machine] == c {
		delete(h.conns, c.machine)
		h.signal()
	}
	h.mu.Unlock()
}

// signal wakes WaitOnline callers. Caller holds h.mu.
func (h *Hub) signal() {
	close(h.changed)
	h.changed = make(chan struct{})
}

func (c *conn) pingLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.ws.CloseNow()
				return
			}
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		}
	}
}

func writeJSON(ctx context.Context, ws *websocket.Conn, mu *sync.Mutex, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	return ws.Write(ctx, websocket.MessageText, b)
}
