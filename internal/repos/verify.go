package repos

import (
	"errors"
	"fmt"
	"os"

	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/workspace"
)

// Problem is why a mapped directory cannot be used.
type Problem string

const (
	ProblemNotAllowed     Problem = "outside the allowed directories"
	ProblemEscapesRoot    Problem = "escapes its allowed directory through a symbolic link"
	ProblemMissing        Problem = "does not exist"
	ProblemNotRepository  Problem = "is not a git repository"
	ProblemOriginMismatch Problem = "has another origin"
)

// VerifyError reports a mapping that does not hold.
type VerifyError struct {
	Dir     string
	Problem Problem
	Err     error
}

func (e *VerifyError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s %s: %v", e.Dir, e.Problem, e.Err)
	}
	return fmt.Sprintf("%s %s", e.Dir, e.Problem)
}

func (e *VerifyError) Unwrap() error { return e.Err }

// Verify checks that dir holds a checkout of repo that a teleport may use:
// it resolves under the policy (skipped when policy is nil), exists, is in a
// git work tree whose top level also resolves under the policy, and that top
// level's origin names repo. It returns the canonical top level.
func Verify(policy *workspace.Policy, git gitx.Runner, dir, repo string) (string, error) {
	canon := dir
	if policy != nil {
		resolved, err := policy.Resolve(dir)
		if err != nil {
			problem := ProblemNotAllowed
			if errors.Is(err, workspace.ErrEscapesRoot) {
				problem = ProblemEscapesRoot
			}
			return "", &VerifyError{Dir: dir, Problem: problem, Err: err}
		}
		canon = resolved
	}
	if info, err := os.Stat(canon); err != nil || !info.IsDir() {
		return "", &VerifyError{Dir: dir, Problem: ProblemMissing}
	}
	if !git.IsRepo(canon) {
		return "", &VerifyError{Dir: dir, Problem: ProblemNotRepository}
	}
	top, err := git.TopLevel(canon)
	if err != nil {
		return "", &VerifyError{Dir: dir, Problem: ProblemNotRepository, Err: err}
	}
	if policy != nil {
		// An allowed root inside a larger work tree: the top level is not ours.
		if _, err := policy.Resolve(top); err != nil {
			return "", &VerifyError{Dir: dir, Problem: ProblemEscapesRoot, Err: err}
		}
	}
	remotes, err := git.Remotes(top)
	if err != nil || !workspace.SameRepository(remotes["origin"], repo) {
		return "", &VerifyError{Dir: dir, Problem: ProblemOriginMismatch, Err: err}
	}
	return top, nil
}
