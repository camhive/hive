// Package protocol defines the wire format between a Hive and the cloud
// gateway: a versioned JSON envelope with a kind, a correlation id and a typed
// payload. Both sides import this package; a change here is one commit that
// either compiles on both sides or does not.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Version is the protocol version this build speaks.
const Version = 1

// Kind names the payload type of an envelope.
type Kind string

// Message kinds.
const (
	KindHello   Kind = "hello"   // hive → cloud, first message
	KindWelcome Kind = "welcome" // cloud → hive, reply to hello
	KindPing    Kind = "ping"    // either direction
	KindPong    Kind = "pong"    // reply to ping
	KindCommand Kind = "command" // cloud → hive, needs an id
	KindResult  Kind = "result"  // hive → cloud, same id as the command
	KindEvent   Kind = "event"   // hive → cloud, fire and forget
)

var kinds = map[Kind]bool{
	KindHello: true, KindWelcome: true, KindPing: true, KindPong: true,
	KindCommand: true, KindResult: true, KindEvent: true,
}

// requiresID lists kinds that must carry a correlation id.
var requiresID = map[Kind]bool{KindCommand: true, KindResult: true}

// Errors.
var (
	ErrUnknownKind        = errors.New("protocol: unknown kind")
	ErrUnsupportedVersion = errors.New("protocol: unsupported version")
	ErrMissingID          = errors.New("protocol: id required for this kind")
)

// Envelope is the unit on the wire.
type Envelope struct {
	V       int             `json:"v"`
	Kind    Kind            `json:"kind"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope wraps payload (may be nil) in an envelope of the given kind.
func NewEnvelope(kind Kind, id string, payload any) (Envelope, error) {
	if !kinds[kind] {
		return Envelope{}, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	if requiresID[kind] && id == "" {
		return Envelope{}, fmt.Errorf("%w: %s", ErrMissingID, kind)
	}
	env := Envelope{V: Version, Kind: kind, ID: id}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		env.Payload = raw
	}
	return env, nil
}

// Unmarshal decodes the payload into v.
func (e Envelope) Unmarshal(v any) error {
	if len(e.Payload) == 0 {
		return errors.New("protocol: empty payload")
	}
	return json.Unmarshal(e.Payload, v)
}

// Encode serialises an envelope.
func Encode(e Envelope) ([]byte, error) { return json.Marshal(e) }

// Decode parses and validates an envelope.
func Decode(raw []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return Envelope{}, fmt.Errorf("protocol: %w", err)
	}
	if e.V != Version {
		return Envelope{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, e.V)
	}
	if !kinds[e.Kind] {
		return Envelope{}, fmt.Errorf("%w: %q", ErrUnknownKind, e.Kind)
	}
	if requiresID[e.Kind] && e.ID == "" {
		return Envelope{}, fmt.Errorf("%w: %s", ErrMissingID, e.Kind)
	}
	return e, nil
}

// Hello is the hive's first message.
type Hello struct {
	HiveID          string `json:"hive_id"`
	Version         string `json:"version"`          // hive software version
	ProtocolVersion int    `json:"protocol_version"` // what the hive speaks
}

// Welcome is the cloud's reply to Hello.
type Welcome struct {
	ServerTime         time.Time     `json:"server_time"`
	PingInterval       time.Duration `json:"ping_interval"`
	MinProtocolVersion int           `json:"min_protocol_version"`
}

// Command asks the hive to do something; the hive answers with a Result of
// the same id.
type Command struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Result answers a Command. Error is a stable machine code (see
// docs/protocol/README.md); Message explains it for humans.
type Result struct {
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Result error codes shared by every command.
const (
	ErrCodeUnsupported = "unsupported" // command unknown to this hive version
	ErrCodeBadArgs     = "bad_args"    // args failed to decode
	ErrCodeInvalid     = "invalid"     // args decoded but violate a rule
	ErrCodeNotFound    = "not_found"
	ErrCodeNotReady    = "not_ready" // the subsystem is not available yet
	ErrCodeInternal    = "internal"
)

// Fail builds a failed Result.
func Fail(code, message string) Result { return Result{Error: code, Message: message} }

// Succeed builds an OK Result with data (nil allowed).
func Succeed(data any) Result {
	r := Result{OK: true}
	if data != nil {
		r.Data, _ = json.Marshal(data)
	}
	return r
}

// Event is a notification from the hive.
type Event struct {
	Name string          `json:"name"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data,omitempty"`
}
