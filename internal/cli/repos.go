package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/config"
	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/paths"
	"github.com/plmercereau/wrekin/internal/repos"
	"github.com/plmercereau/wrekin/internal/workspace"
)

const sourceDeclared = "declared"

// repoMapping is one line of `wrekin repos list`.
type repoMapping struct {
	Repository string     `json:"repository"`
	Key        string     `json:"key"`
	Path       string     `json:"path"`
	Source     string     `json:"source"`
	Valid      bool       `json:"valid"`
	Problem    string     `json:"problem,omitempty"`
	Shadowed   bool       `json:"shadowed,omitempty"`
	AddedAt    *time.Time `json:"addedAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

func newReposCommand(a *app) *cobra.Command {
	var asJSON bool
	list := func(cmd *cobra.Command, args []string) error { return listRepos(a, asJSON) }
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "List, forget or pin the repository to checkout mappings",
		Long: `Wrekin finds the checkout of a repository in this order: the pins declared in
settings.json (workspace.repositories), the mappings the daemon learned
(` + "`~/.local/state/wrekin/repositories.json`" + `: checkouts it found or cloned), then a scan
of the allowed directories. Without a subcommand, lists the mappings.`,
		Args: cobra.NoArgs,
		RunE: list,
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the mappings as JSON")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List the declared and learned mappings and whether they still hold",
		Args:  cobra.NoArgs,
		RunE:  list,
	}
	listCmd.Flags().BoolVar(&asJSON, "json", false, "print the mappings as JSON")

	forgetCmd := &cobra.Command{
		Use:               "forget <repository>",
		Short:             "Remove a learned mapping (a declared pin lives in settings.json)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeLearnedRepository(a),
		RunE:              func(cmd *cobra.Command, args []string) error { return forgetRepo(cmd, a, args[0]) },
	}
	pinCmd := &cobra.Command{
		Use:               "pin <repository>",
		Short:             "Print the settings snippet that declares a learned mapping (writes nothing)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeLearnedRepository(a),
		RunE:              func(cmd *cobra.Command, args []string) error { return pinRepo(a, args[0]) },
	}
	cmd.AddCommand(listCmd, forgetCmd, pinCmd)
	return cmd
}

// declaredKey finds the declared pin of key, whatever spelling the settings
// use: it returns the settings key and the directory.
func declaredKey(s config.Settings, key string) (string, string, bool) {
	for repo, dir := range s.Workspace.Repositories {
		if k, ok := workspace.RepoKey(repo); ok && k == key {
			return repo, dir, true
		}
	}
	return "", "", false
}

func listRepos(a *app, asJSON bool) error {
	settings, err := config.Load(a.settingsPath)
	if err != nil {
		return err
	}
	learned, err := repos.New(paths.RepositoriesFile()).List()
	if err != nil {
		return err
	}
	// The policy needs the allowed directories to exist; without it the
	// paths are still checked for a checkout of the right repository.
	policy, err := workspace.NewPolicy(settings.Workspace.AllowedDirectories, settings.Workspace.DefaultDirectory)
	if err != nil {
		fmt.Fprintln(a.stderr, "warning: the allowed directories cannot be checked:", err)
		policy = nil
	}
	git := gitx.Runner{}
	check := func(m *repoMapping) {
		if _, err := repos.Verify(policy, git, m.Path, m.Repository); err != nil {
			var ve *repos.VerifyError
			if errors.As(err, &ve) {
				m.Problem = string(ve.Problem)
			} else {
				m.Problem = err.Error()
			}
			return
		}
		m.Valid = true
	}

	out := []repoMapping{}
	declared := map[string]bool{}
	for repo, dir := range settings.Workspace.Repositories {
		key, _ := workspace.RepoKey(repo)
		declared[key] = true
		m := repoMapping{Repository: repo, Key: key, Path: dir, Source: sourceDeclared}
		check(&m)
		out = append(out, m)
	}
	for _, e := range learned {
		added, used := e.AddedAt, e.LastUsedAt
		m := repoMapping{Repository: e.Repository, Key: e.Key, Path: e.Path, Source: string(e.Source), Shadowed: declared[e.Key], AddedAt: &added, LastUsedAt: &used}
		check(&m)
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Source == sourceDeclared && out[j].Source != sourceDeclared
	})

	if asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(out) == 0 {
		fmt.Fprintln(a.stdout, "No repository mappings yet. Declare some in settings.json (workspace.repositories); the daemon learns the others as it finds or clones checkouts.")
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tPATH\tSOURCE\tSTATUS\tLAST USED")
	for _, m := range out {
		status := "ok"
		if !m.Valid {
			status = "invalid: " + m.Problem
		}
		if m.Shadowed {
			status += " (shadowed by the declared pin)"
		}
		used := "-"
		if m.LastUsedAt != nil {
			used = m.LastUsedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.Repository, tildePath(m.Path), m.Source, status, used)
	}
	return w.Flush()
}

func forgetRepo(cmd *cobra.Command, a *app, repo string) error {
	key, ok := workspace.RepoKey(repo)
	if !ok {
		return fmt.Errorf("%q is not a repository reference", repo)
	}
	settings, err := config.Load(a.settingsPath)
	if err != nil {
		return err
	}
	if declared, dir, ok := declaredKey(settings, key); ok {
		return fmt.Errorf("%s is pinned to %s in %s (workspace.repositories); Wrekin never writes that file: remove the pin there (or in your Home Manager configuration) and restart the daemon", declared, tildePath(dir), tildePath(a.settingsPath))
	}
	// The daemon owns the learned file while it runs: ask it, so that its
	// next write does not bring the entry back.
	var res control.ReposForgetResult
	err = control.NewClient(paths.SocketFile()).Call(cmd.Context(), control.MethodReposForget, control.ReposForgetParams{Repository: repo}, &res)
	if errors.Is(err, control.ErrNotRunning) {
		entry, removed, ferr := repos.New(paths.RepositoriesFile()).Forget(repo)
		if ferr != nil {
			return ferr
		}
		res = control.ReposForgetResult{Removed: removed, Repository: entry.Repository, Path: entry.Path}
	} else if err != nil {
		return err
	}
	if !res.Removed {
		fmt.Fprintf(a.stdout, "No learned mapping for %s.\n", repo)
		return nil
	}
	fmt.Fprintf(a.stdout, "Forgot %s -> %s.\n", res.Repository, tildePath(res.Path))
	return nil
}

func pinRepo(a *app, repo string) error {
	key, ok := workspace.RepoKey(repo)
	if !ok {
		return fmt.Errorf("%q is not a repository reference", repo)
	}
	settings, err := config.Load(a.settingsPath)
	if err != nil {
		return err
	}
	if declared, dir, ok := declaredKey(settings, key); ok {
		fmt.Fprintf(a.stdout, "%s is already declared in %s: %s\n", declared, tildePath(a.settingsPath), tildePath(dir))
		return nil
	}
	entry, ok, err := repos.New(paths.RepositoriesFile()).Lookup(repo)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no learned mapping for %s (`wrekin repos` lists them)", repo)
	}
	name := pinName(entry)
	dir := tildePath(entry.Path)
	snippet, err := json.MarshalIndent(map[string]any{"workspace": map[string]any{"repositories": map[string]string{name: dir}}}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Add to %s (merging with the keys already there):\n\n%s\n\n", tildePath(a.settingsPath), snippet)
	fmt.Fprintf(a.stdout, "or, with Home Manager:\n\nservices.wrekin.settings.workspace.repositories.%s = %s;\n\n", nixString(name), nixString(dir))
	fmt.Fprintln(a.stdout, "Then restart the daemon (wrekin stop && wrekin start).")
	return nil
}

var ownerNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// pinName is the shortest key for a pin: "owner/name" for GitHub, the
// recorded URL otherwise.
func pinName(e repos.Entry) string {
	if rest, ok := strings.CutPrefix(e.Key, "github.com/"); ok && ownerNameRe.MatchString(rest) {
		return rest
	}
	return e.Repository
}

// tildePath shortens the home directory to "~" for display and snippets.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	home = filepath.Clean(home)
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return p
}

// nixString quotes s as a Nix string literal.
func nixString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "${", `\${`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
