// Package jobs owns the teleport jobs: their public (protocol) view, the
// local-only bookkeeping, the persisted store and the state machine that
// drives a job from accepted to ready or failed.
package jobs

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"time"

	"github.com/plmercereau/wrekin/internal/protocol"
)

// Job is a protocol.Job plus the local-only fields the daemon needs to
// resume work and to clean up only what Wrekin created. The local fields are
// persisted in jobs.json and dropped by Wire().
type Job struct {
	protocol.Job

	// CreatedClone is true when Wrekin cloned the repository for this job.
	CreatedClone bool
	// CreatedWorktree is true when Wrekin created the isolated worktree.
	CreatedWorktree bool
	// CheckoutDir is the repository checkout the worktree was made from.
	CheckoutDir string
	// ClonePath is where an approved clone goes.
	ClonePath string
	// AttentionReason says why the job waits: "clone" or "trust".
	AttentionReason string
	// ApprovedAt is set when the user approved the repository.
	ApprovedAt *time.Time
	// Params are the original teleport parameters.
	Params protocol.TeleportParams
}

// localFields is the JSON shape of the local-only part.
type localFields struct {
	CreatedClone    bool                    `json:"createdClone"`
	CreatedWorktree bool                    `json:"createdWorktree"`
	CheckoutDir     string                  `json:"checkoutDir,omitempty"`
	ClonePath       string                  `json:"clonePath,omitempty"`
	AttentionReason string                  `json:"attentionReason,omitempty"`
	ApprovedAt      *time.Time              `json:"approvedAt,omitempty"`
	Params          protocol.TeleportParams `json:"params"`
}

// MarshalJSON merges the wire view and the local fields into one object.
func (j Job) MarshalJSON() ([]byte, error) {
	wire, err := json.Marshal(j.Job)
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(wire, &merged); err != nil {
		return nil, err
	}
	local, err := json.Marshal(localFields{
		CreatedClone: j.CreatedClone, CreatedWorktree: j.CreatedWorktree, CheckoutDir: j.CheckoutDir,
		ClonePath: j.ClonePath, AttentionReason: j.AttentionReason, ApprovedAt: j.ApprovedAt, Params: j.Params,
	})
	if err != nil {
		return nil, err
	}
	var localMap map[string]json.RawMessage
	if err := json.Unmarshal(local, &localMap); err != nil {
		return nil, err
	}
	for k, v := range localMap {
		merged[k] = v
	}
	return json.Marshal(merged)
}

// UnmarshalJSON reads both parts from one object.
func (j *Job) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &j.Job); err != nil {
		return err
	}
	var local localFields
	if err := json.Unmarshal(data, &local); err != nil {
		return err
	}
	j.CreatedClone = local.CreatedClone
	j.CreatedWorktree = local.CreatedWorktree
	j.CheckoutDir = local.CheckoutDir
	j.ClonePath = local.ClonePath
	j.AttentionReason = local.AttentionReason
	j.ApprovedAt = local.ApprovedAt
	j.Params = local.Params
	return nil
}

// Wire is the view sent to the relay.
func (j *Job) Wire() protocol.Job { return j.Job }

// clone returns a deep copy.
func (j *Job) clone() *Job {
	c := *j
	if j.Repository != nil {
		r := *j.Repository
		c.Repository = &r
	}
	if j.Session != nil {
		s := *j.Session
		c.Session = &s
	}
	if j.Attention != nil {
		a := *j.Attention
		c.Attention = &a
	}
	if j.Ready != nil {
		r := *j.Ready
		c.Ready = &r
	}
	if j.Error != nil {
		e := *j.Error
		c.Error = &e
	}
	if j.ApprovedAt != nil {
		t := *j.ApprovedAt
		c.ApprovedAt = &t
	}
	return &c
}

// ShortID is the suffix used in session and branch names: the last eight
// characters of the cloud session id, lower-case.
func ShortID(cloudSessionID string) string {
	s := strings.ToLower(cloudSessionID)
	if len(s) > 8 {
		s = s[len(s)-8:]
	}
	return s
}

// SessionName is the terminal session name for a cloud session.
func SessionName(cloudSessionID string) string { return sessionPrefix + ShortID(cloudSessionID) }

// sessionPrefix starts every terminal session Wrekin creates.
const sessionPrefix = "wrekin-"

// BranchName is the isolated worktree branch for a cloud session.
func BranchName(cloudSessionID string) string { return "wrekin/" + ShortID(cloudSessionID) }

const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// newJobID returns "job_" followed by 20 random Crockford base32 characters.
func newJobID() string {
	var buf [20]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	for i := range buf {
		buf[i] = idAlphabet[int(buf[i])%len(idAlphabet)]
	}
	return "job_" + string(buf[:])
}
