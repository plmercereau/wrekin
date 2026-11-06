package cli

import (
	"bytes"
	"strings"
	"testing"
)

// complete runs cobra's hidden __complete command, as the shell scripts do,
// and returns the candidates (the last line is the directive).
func complete(t *testing.T, a *app, args ...string) []string {
	t.Helper()
	var out bytes.Buffer
	a.stdout, a.stderr = &out, &bytes.Buffer{}
	a.stdin = strings.NewReader("")
	root := newRootCommand(a)
	root.SetArgs(append([]string{"__complete"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("__complete %v: %v", args, err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], ":") {
		t.Fatalf("no directive in %q", out.String())
	}
	if lines[len(lines)-1] != ":4" { // ShellCompDirectiveNoFileComp: never offer files
		t.Errorf("directive = %s, want :4", lines[len(lines)-1])
	}
	return lines[:len(lines)-1]
}

func completionApp() *app {
	return &app{
		version:          "t",
		completeSessions: func() []string { return []string{"wrekin-b", "wrekin-a"} },
		completeJobs:     func() []string { return []string{"job_1"} },
		completeRepos:    func() []string { return []string{"github.com/plmercereau/wrekin", "github.com/o/other"} },
	}
}

func TestCompletionOffersSessionsJobsAndRepositories(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"attach", ""}, "wrekin-a wrekin-b"},
		{[]string{"attach", "wrekin-a", ""}, ""},
		{[]string{"kill", ""}, "job_1 wrekin-a wrekin-b"},
		{[]string{"kill", "wrekin-a", ""}, "job_1 wrekin-b"},
		{[]string{"kill", "wrekin-"}, "wrekin-a wrekin-b"},
		{[]string{"kill", "--all", ""}, ""},
		{[]string{"repos", "forget", "github.com/p"}, "github.com/plmercereau/wrekin"},
		{[]string{"repos", "pin", ""}, "github.com/o/other github.com/plmercereau/wrekin"},
	} {
		if got := strings.Join(complete(t, completionApp(), tc.args...), " "); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestCompletionScriptsAreGenerated(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out, err := run(t, "completion", shell)
		if err != nil || !strings.Contains(out, "wrekin") || len(out) < 500 {
			t.Errorf("%s: err %v, %d bytes", shell, err, len(out))
		}
	}
}
