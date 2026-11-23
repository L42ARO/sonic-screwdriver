// Package hub holds the WebSocket connections from local-agents and lets the rest of the
// compute-box send them actions.
//
// Wire protocol (JSON text messages):
//
//	agent -> box  {"type":"hello","token":"<machine token>","version":"...","screen":{...}}
//	box -> agent  {"type":"welcome","machine":"<name>"}  or  {"type":"error","code":"...","message":"..."} then close
//	box -> agent  {"type":"action","id":"a-1","action":{"action":"click","x":10,"y":20}}
//	agent -> box  {"type":"result","id":"a-1","result":{...ActionResult...}}
//	box -> agent  {"type":"job_start","job_id":"...","operation":"..."}
//	box -> agent  {"type":"status","text":"..."}
//	box -> agent  {"type":"job_end","job_id":"..."}
//
// The box sends WebSocket pings every 15 s; the agent treats 45 s of silence as a dead
// connection and reconnects.
package hub

import "encoding/json"

// Screen is the agent's screenshot geometry.
type Screen struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

type hello struct {
	Probe      bool   `json:"probe,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Type       string `json:"type"`
	Machine    string `json:"machine"`
	Token      string `json:"token"`
	Version    string `json:"version"`
	Screen     Screen `json:"screen"`
}

type inbound struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
}

type actionMsg struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Action json.RawMessage `json:"action"`
}

// Shot is a screenshot returned with an action result.
type Shot struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Scale     float64 `json:"scale"`
	Region    []int   `json:"region,omitempty"`
	PNGBase64 string  `json:"png_base64"`
}

// Result is what the agent reports after running one action.
type Result struct {
	Data       json.RawMessage `json:"data,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	OK         bool            `json:"ok"`
	Action     string          `json:"action"`
	ElapsedMS  int             `json:"elapsed_ms"`
	Error      string          `json:"error,omitempty"`
	Unsettled  bool            `json:"unsettled,omitempty"`
	Screenshot *Shot           `json:"screenshot,omitempty"`
}
