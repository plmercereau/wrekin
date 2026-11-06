// Package protocol holds the Go types of the Wrekin node protocol, version 1,
// as specified in protocol/PROTOCOL.md. It knows nothing about any harness.
package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// Version is the protocol version carried in every frame.
const Version = 1

// Message types.
const (
	TypeHello     = "hello"
	TypeChallenge = "challenge"
	TypeAuth      = "auth"
	TypeWelcome   = "welcome"
	TypeError     = "error"
	TypePing      = "ping"
	TypePong      = "pong"
	TypeCommand   = "command"
	TypeResult    = "result"
)

// Command names.
const (
	CmdTeleport          = "teleport"
	CmdGetTeleportStatus = "get_teleport_status"
	CmdApproveRepository = "approve_repository"
	CmdListJobs          = "list_jobs"
	CmdPing              = "ping"
)

// WebSocket close codes sent by the relay on fatal conditions.
const (
	CloseProtocolError = 4400
	CloseAuthFailed    = 4401
	CloseUnknownNode   = 4404
	CloseReplaced      = 4409
	CloseTooMany       = 4429
)

// Error codes, shared by the node, the relay and the MCP tools.
const (
	CodeNodeOffline         = "node_offline"
	CodeNodeTimeout         = "node_timeout"
	CodeUnknownNode         = "unknown_node"
	CodeInvalidParams       = "invalid_params"
	CodeUnknownJob          = "unknown_job"
	CodeRepositoryRequired  = "repository_required"
	CodeDirectoryNotAllowed = "directory_not_allowed"
	CodePathEscapesRoot     = "path_escapes_root"
	CodeHarnessUnavailable  = "harness_unavailable"
	CodeTerminalUnavailable = "terminal_unavailable"
	CodeConcurrencyLimit    = "concurrency_limit"
	CodeAttentionNotPending = "attention_not_pending"
	CodeNotApproved         = "not_approved"
	CodeCloneFailed         = "clone_failed"
	CodeGitPromptBlocked    = "git_prompt_blocked"
	CodeWorktreeFailed      = "worktree_failed"
	CodeHarnessExited       = "harness_exited"
	CodeSessionGone         = "session_gone"
	CodeInternal            = "internal"
)

// Envelope is the part every frame carries.
type Envelope struct {
	V    int    `json:"v"`
	Type string `json:"type"`
}

// HarnessInfo advertises one harness in the hello message.
type HarnessInfo struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
}

// Platform describes the node's machine.
type Platform struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
}

// Hello is the first frame, node to relay.
type Hello struct {
	Envelope
	NodeID       string        `json:"nodeId"`
	PublicKey    string        `json:"publicKey"`
	Name         string        `json:"name"`
	AgentVersion string        `json:"agentVersion"`
	Platform     Platform      `json:"platform"`
	Harnesses    []HarnessInfo `json:"harnesses"`
}

// Challenge is the relay's nonce, to be signed.
type Challenge struct {
	Envelope
	Nonce string `json:"nonce"`
}

// Auth carries the node's signature over the challenge.
type Auth struct {
	Envelope
	Signature string `json:"signature"`
}

// Welcome completes the handshake.
type Welcome struct {
	Envelope
	AccountID   string `json:"accountId"`
	NodeID      string `json:"nodeId"`
	PingSeconds int    `json:"pingSeconds"`
}

// ErrorMessage is sent by the relay before a fatal close.
type ErrorMessage struct {
	Envelope
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Ping is sent by the node every pingSeconds.
type Ping struct {
	Envelope
}

// Pong answers a ping.
type Pong struct {
	Envelope
}

// Command is a request from the relay; the node answers it exactly once.
type Command struct {
	Envelope
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Params json.RawMessage `json:"params"`
}

// DecodeParams unmarshals the command parameters into v. Absent params
// decode as an empty object.
func (c *Command) DecodeParams(v any) error {
	if len(c.Params) == 0 {
		return json.Unmarshal([]byte("{}"), v)
	}
	return json.Unmarshal(c.Params, v)
}

// Result answers a command.
type Result struct {
	Envelope
	ID    string    `json:"id"`
	OK    bool      `json:"ok"`
	Data  any       `json:"data,omitempty"`
	Error *JobError `json:"error,omitempty"`
}

// UnknownMessage is returned by Decode for a type this version does not
// know; both sides ignore such frames.
type UnknownMessage struct {
	Envelope
}

// State is a job state.
type State string

// The six job states.
const (
	StateAccepted       State = "accepted"
	StatePreparing      State = "preparing"
	StateNeedsAttention State = "needs_attention"
	StateLaunching      State = "launching"
	StateReady          State = "ready"
	StateFailed         State = "failed"
)

// Job is the public view of a teleport job. Sub-objects are null when not
// applicable; nothing in it is raw terminal output.
type Job struct {
	ID             string      `json:"id"`
	Harness        string      `json:"harness"`
	CloudSessionID string      `json:"cloudSessionId"`
	State          State       `json:"state"`
	Repository     *Repository `json:"repository"`
	Workdir        string      `json:"workdir"`
	Session        *Session    `json:"session"`
	Attention      *Attention  `json:"attention"`
	Ready          *Ready      `json:"ready"`
	Error          *JobError   `json:"error"`
	CreatedAt      time.Time   `json:"createdAt"`
	UpdatedAt      time.Time   `json:"updatedAt"`
}

// MarshalJSON emits workdir as null when it is empty.
func (j Job) MarshalJSON() ([]byte, error) {
	type alias Job
	wire := struct {
		*alias
		Workdir *string `json:"workdir"`
	}{alias: (*alias)(&j)}
	if j.Workdir != "" {
		wire.Workdir = &j.Workdir
	}
	return json.Marshal(wire)
}

// UnmarshalJSON accepts a null workdir.
func (j *Job) UnmarshalJSON(data []byte) error {
	type alias Job
	wire := struct {
		*alias
		Workdir *string `json:"workdir"`
	}{alias: (*alias)(j)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Workdir != nil {
		j.Workdir = *wire.Workdir
	} else {
		j.Workdir = ""
	}
	return nil
}

// Repository describes the repository a job works on.
type Repository struct {
	Input string `json:"input"`
	URL   string `json:"url"`
	Name  string `json:"name"`
}

// Session describes the terminal session that hosts the harness.
type Session struct {
	Terminal string `json:"terminal"`
	Name     string `json:"name"`
	Attach   string `json:"attach"`
}

// AttentionApproveRepository is the only attention kind in the MVP.
const AttentionApproveRepository = "approve_repository"

// Attention explains what the user must confirm in needs_attention.
type Attention struct {
	Kind          string `json:"kind"`
	Message       string `json:"message"`
	RepositoryURL string `json:"repositoryUrl"`
	ProposedPath  string `json:"proposedPath"`
}

// Ready reports whether the harness prompt was actually seen.
type Ready struct {
	Confirmed bool   `json:"confirmed"`
	Detail    string `json:"detail"`
}

// JobError is a stable code plus a sentence the node writes.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *JobError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

// TeleportParams are the parameters of the teleport command.
type TeleportParams struct {
	Harness        string `json:"harness"`
	CloudSessionID string `json:"cloudSessionId"`
	Repository     string `json:"repository,omitempty"`
	Directory      string `json:"directory,omitempty"`
	Relaunch       bool   `json:"relaunch,omitempty"`
}

// StatusParams are the parameters of get_teleport_status.
type StatusParams struct {
	JobID string `json:"jobId"`
}

// ApproveParams are the parameters of approve_repository.
type ApproveParams struct {
	JobID    string `json:"jobId"`
	Approved bool   `json:"approved"`
}

// ListJobsResult is the result of list_jobs.
type ListJobsResult struct {
	Jobs []Job `json:"jobs"`
}

// PingResult is the result of the ping command.
type PingResult struct {
	At string `json:"at"`
}

// NewResult builds a successful result.
func NewResult(id string, data any) Result {
	return Result{Envelope: Envelope{V: Version, Type: TypeResult}, ID: id, OK: true, Data: data}
}

// NewErrorResult builds a failed result.
func NewErrorResult(id, code, msg string) Result {
	return Result{Envelope: Envelope{V: Version, Type: TypeResult}, ID: id, OK: false, Error: &JobError{Code: code, Message: msg}}
}

// NewPing builds a ping frame.
func NewPing() Ping { return Ping{Envelope{V: Version, Type: TypePing}} }

// NewPong builds a pong frame.
func NewPong() Pong { return Pong{Envelope{V: Version, Type: TypePong}} }

// NewAuth builds an auth frame.
func NewAuth(signature string) Auth {
	return Auth{Envelope: Envelope{V: Version, Type: TypeAuth}, Signature: signature}
}

// Decode parses one frame and returns the typed message for its type:
// *Hello, *Challenge, *Auth, *Welcome, *ErrorMessage, *Ping, *Pong,
// *Command, *Result, or *UnknownMessage for a type this version ignores.
func Decode(data []byte) (any, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("malformed frame: %w", err)
	}
	if env.V != Version {
		return nil, fmt.Errorf("unsupported protocol version %d", env.V)
	}
	var msg any
	switch env.Type {
	case TypeHello:
		msg = &Hello{}
	case TypeChallenge:
		msg = &Challenge{}
	case TypeAuth:
		msg = &Auth{}
	case TypeWelcome:
		msg = &Welcome{}
	case TypeError:
		msg = &ErrorMessage{}
	case TypePing:
		msg = &Ping{}
	case TypePong:
		msg = &Pong{}
	case TypeCommand:
		msg = &Command{}
	case TypeResult:
		msg = &Result{}
	default:
		return &UnknownMessage{Envelope: env}, nil
	}
	if err := json.Unmarshal(data, msg); err != nil {
		return nil, fmt.Errorf("malformed %s frame: %w", env.Type, err)
	}
	return msg, nil
}
