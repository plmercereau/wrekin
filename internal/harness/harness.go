// Package harness defines the adapter boundary between Wrekin and the AI
// coding harness it launches. Only internal/harness/claude knows about
// Claude Code.
package harness

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// ErrUnknown means no adapter is registered for the harness id.
var ErrUnknown = errors.New("unknown harness")

// Info is what Detect learns about the installed harness.
type Info struct {
	Available bool
	Version   string
}

// Harness is one adapter.
type Harness interface {
	// ID is the protocol harness id ("claude-code").
	ID() string
	// Detect finds the binary and its version; a missing binary is
	// Info{Available: false}, not an error.
	Detect() (Info, error)
	// Trusted reports whether the harness already trusts dir.
	Trusted(dir string) (bool, error)
	// Trust records dir as trusted, so the harness does not ask on launch.
	Trust(dir string) error
	// LaunchArgs is the argv that resumes the cloud session in the current
	// directory.
	LaunchArgs(cloudSessionID string) []string
	// ReadyPattern matches the pane once the harness prompt is visible.
	ReadyPattern() *regexp.Regexp
	// ExitedIsFailure says whether the harness exiting before readiness is a
	// failure (true for an interactive harness).
	ExitedIsFailure() bool
}

// Registry maps harness ids to adapters.
type Registry struct {
	byID map[string]Harness
}

// NewRegistry builds a registry from adapters.
func NewRegistry(hs ...Harness) *Registry {
	r := &Registry{byID: map[string]Harness{}}
	for _, h := range hs {
		r.byID[h.ID()] = h
	}
	return r
}

// Get returns the adapter for id.
func (r *Registry) Get(id string) (Harness, error) {
	h, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknown, id)
	}
	return h, nil
}

// All returns the adapters sorted by id.
func (r *Registry) All() []Harness {
	out := make([]Harness, 0, len(r.byID))
	for _, h := range r.byID {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// DetectAll runs Detect on every adapter; ctx is reserved for cancellation of
// slow detections.
func (r *Registry) DetectAll(ctx context.Context) map[string]Info {
	out := map[string]Info{}
	for _, h := range r.All() {
		if ctx.Err() != nil {
			break
		}
		info, err := h.Detect()
		if err != nil {
			info = Info{}
		}
		out[h.ID()] = info
	}
	return out
}
