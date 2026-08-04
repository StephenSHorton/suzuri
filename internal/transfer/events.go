// Package transfer shells out to the suzuri-transfer (hato) engine over NDJSON.
// See https://github.com/StephenSHorton/hato/blob/main/docs/machine-mode.md
package transfer

import (
	"encoding/json"
	"fmt"
)

// ProtocolV is the NDJSON envelope version we understand.
const ProtocolV = 1

// Event is one NDJSON line from the engine (flexible fields).
type Event struct {
	V     int    `json:"v"`
	Event string `json:"event"`

	// Common / send
	Ticket    string `json:"ticket,omitempty"`
	Relays    *int   `json:"relays,omitempty"`
	IPs       *int   `json:"ips,omitempty"`
	RelayOnly *bool  `json:"relay_only,omitempty"`
	Path      string `json:"path,omitempty"`
	Reason    string `json:"reason,omitempty"`

	// Progress / receive
	Done        *uint64 `json:"done,omitempty"`
	Total       *uint64 `json:"total,omitempty"`
	TotalBytes  *uint64 `json:"total_bytes,omitempty"`
	AlreadyHad  *uint64 `json:"already_had,omitempty"`
	OutDir      string  `json:"out_dir,omitempty"`
	Dir         string  `json:"dir,omitempty"`

	// Error
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`

	// Identity / contacts
	DisplayName    string `json:"display_name,omitempty"`
	EndpointID     string `json:"endpoint_id,omitempty"`
	EndpointShort  string `json:"endpoint_short,omitempty"`
	ConfigDir      string `json:"config_dir,omitempty"`
	Contacts       any    `json:"contacts,omitempty"`

	// Pairing / codes
	Kind      string `json:"kind,omitempty"`
	ContactID string `json:"contact_id,omitempty"`
	Name      string `json:"name,omitempty"`
	SAS       string `json:"sas,omitempty"`
	Label     string `json:"label,omitempty"`
	Bytes     *uint64 `json:"bytes,omitempty"`
	From      string  `json:"from,omitempty"`
}

// ParseEvent decodes one NDJSON line.
func ParseEvent(line []byte) (Event, error) {
	var ev Event
	if err := json.Unmarshal(line, &ev); err != nil {
		return Event{}, fmt.Errorf("ndjson: %w", err)
	}
	if ev.Event == "" {
		return Event{}, fmt.Errorf("ndjson: missing event field")
	}
	return ev, nil
}
