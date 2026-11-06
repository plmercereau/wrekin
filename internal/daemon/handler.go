package daemon

import (
	"context"
	"time"

	"github.com/plmercereau/wrekin/internal/jobs"
	"github.com/plmercereau/wrekin/internal/protocol"
)

// RunnerAPI is the part of jobs.Runner the command handler uses.
type RunnerAPI interface {
	Teleport(ctx context.Context, p protocol.TeleportParams) (*jobs.Job, *protocol.JobError)
	Status(jobID string) (*jobs.Job, *protocol.JobError)
	Approve(jobID string, approved bool) (*jobs.Job, *protocol.JobError)
	List() ([]*jobs.Job, *protocol.JobError)
}

// Handler maps relay commands to runner calls.
type Handler struct {
	runner RunnerAPI
	now    func() time.Time
}

// NewHandler builds a Handler; now may be nil.
func NewHandler(runner RunnerAPI, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{runner: runner, now: now}
}

// Handle implements relay.Handler.
func (h *Handler) Handle(ctx context.Context, cmd protocol.Command) protocol.Result {
	switch cmd.Name {
	case protocol.CmdTeleport:
		var p protocol.TeleportParams
		if err := cmd.DecodeParams(&p); err != nil {
			return protocol.NewErrorResult(cmd.ID, protocol.CodeInvalidParams, "teleport parameters are malformed")
		}
		job, jerr := h.runner.Teleport(ctx, p)
		return jobResult(cmd.ID, job, jerr)
	case protocol.CmdGetTeleportStatus:
		var p protocol.StatusParams
		if err := cmd.DecodeParams(&p); err != nil || p.JobID == "" {
			return protocol.NewErrorResult(cmd.ID, protocol.CodeInvalidParams, "jobId is required")
		}
		job, jerr := h.runner.Status(p.JobID)
		return jobResult(cmd.ID, job, jerr)
	case protocol.CmdApproveRepository:
		var p protocol.ApproveParams
		if err := cmd.DecodeParams(&p); err != nil || p.JobID == "" {
			return protocol.NewErrorResult(cmd.ID, protocol.CodeInvalidParams, "jobId is required")
		}
		job, jerr := h.runner.Approve(p.JobID, p.Approved)
		return jobResult(cmd.ID, job, jerr)
	case protocol.CmdListJobs:
		list, jerr := h.runner.List()
		if jerr != nil {
			return protocol.NewErrorResult(cmd.ID, jerr.Code, jerr.Message)
		}
		out := protocol.ListJobsResult{Jobs: make([]protocol.Job, 0, len(list))}
		for _, j := range list {
			out.Jobs = append(out.Jobs, j.Wire())
		}
		return protocol.NewResult(cmd.ID, out)
	case protocol.CmdPing:
		return protocol.NewResult(cmd.ID, protocol.PingResult{At: h.now().UTC().Format(time.RFC3339)})
	default:
		return protocol.NewErrorResult(cmd.ID, protocol.CodeInvalidParams, "unknown command "+cmd.Name)
	}
}

func jobResult(id string, job *jobs.Job, jerr *protocol.JobError) protocol.Result {
	if jerr != nil {
		return protocol.NewErrorResult(id, jerr.Code, jerr.Message)
	}
	return protocol.NewResult(id, job.Wire())
}
