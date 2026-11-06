// Package workspace enforces the directory policy: only canonical paths under
// the allowed roots may be used, symbolic links cannot lead outside, and clone
// destinations are proposed under the default root. It also parses the
// repository references the relay sends.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/plmercereau/wrekin/internal/config"
)

// ErrNotAllowed means the path is outside every allowed root.
var ErrNotAllowed = errors.New("directory is outside every allowed root")

// ErrEscapesRoot means the path lies under a root lexically but its canonical
// form (after following symbolic links) does not.
var ErrEscapesRoot = errors.New("path escapes its root through a symbolic link")

// Policy is the set of canonical, absolute roots a teleport may use.
type Policy struct {
	Roots       []string
	DefaultRoot string
}

// NewPolicy canonicalises the allowed directories (which must exist) and the
// default directory (which must be under one of them; empty means the first
// root).
func NewPolicy(allowed []string, defaultDir string) (*Policy, error) {
	if len(allowed) == 0 {
		return nil, errors.New("no allowed directories configured")
	}
	p := &Policy{}
	for _, dir := range allowed {
		abs, err := filepath.Abs(config.ExpandHome(dir))
		if err != nil {
			return nil, err
		}
		canon, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("allowed directory %s: %w", dir, err)
		}
		info, err := os.Stat(canon)
		if err != nil {
			return nil, fmt.Errorf("allowed directory %s: %w", dir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("allowed directory %s is not a directory", dir)
		}
		p.Roots = append(p.Roots, canon)
	}
	if defaultDir == "" {
		p.DefaultRoot = p.Roots[0]
		return p, nil
	}
	abs, err := filepath.Abs(config.ExpandHome(defaultDir))
	if err != nil {
		return nil, err
	}
	canon, err := canonical(abs)
	if err != nil {
		return nil, fmt.Errorf("default directory %s: %w", defaultDir, err)
	}
	if p.rootOf(canon) == "" {
		return nil, fmt.Errorf("default directory %s is outside every allowed directory", defaultDir)
	}
	p.DefaultRoot = canon
	return p, nil
}

// Resolve returns the canonical form of p when it is allowed. A path that
// lexically sits under a root but whose canonical form does not is
// ErrEscapesRoot; a path under no root is ErrNotAllowed. p need not exist.
func (p *Policy) Resolve(in string) (string, error) {
	abs, err := filepath.Abs(config.ExpandHome(in))
	if err != nil {
		return "", err
	}
	lexical := filepath.Clean(abs)
	canon, err := canonical(lexical)
	if err != nil {
		return "", err
	}
	claimed := p.claimedRoot(lexical)
	switch {
	case claimed != "":
		if p.rootOf(canon) != claimed {
			return "", fmt.Errorf("%w: %s", ErrEscapesRoot, in)
		}
		return canon, nil
	case p.rootOf(canon) != "":
		return canon, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrNotAllowed, in)
	}
}

// claimedRoot finds the root the path presents itself as being under: the
// first ancestor (the path included) whose canonical form is a root.
func (p *Policy) claimedRoot(lexical string) string {
	for cur := lexical; ; cur = filepath.Dir(cur) {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for _, r := range p.Roots {
				if resolved == r {
					return r
				}
			}
		}
		if filepath.Dir(cur) == cur {
			return ""
		}
	}
}

// rootOf returns the root a canonical path is under, or "".
func (p *Policy) rootOf(canon string) string {
	for _, r := range p.Roots {
		if canon == r || strings.HasPrefix(canon, r+string(filepath.Separator)) {
			return r
		}
	}
	return ""
}

// ProposeClonePath returns <DefaultRoot>/<repoName>, with a -2, -3, ... suffix
// when the plain name is already taken.
func (p *Policy) ProposeClonePath(repoName string) string {
	name := sanitiseName(repoName)
	candidate := filepath.Join(p.DefaultRoot, name)
	for i := 2; exists(candidate); i++ {
		candidate = filepath.Join(p.DefaultRoot, fmt.Sprintf("%s-%d", name, i))
	}
	return candidate
}

func sanitiseName(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".git")
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Trim(name, ". ")
	if name == "" || name == "/" {
		return "repository"
	}
	return name
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// canonical resolves symbolic links in the longest existing prefix of p and
// re-appends the rest, so paths that do not exist yet can be checked too.
func canonical(p string) (string, error) {
	rest := ""
	for cur := p; ; {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// RepoRef is a parsed repository reference.
type RepoRef struct {
	Input string
	URL   string
	Name  string
}

var (
	shorthandRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	scpLikeRe   = regexp.MustCompile(`^(?:[A-Za-z0-9_.-]+@)?([A-Za-z0-9_.-]+):([^/:][^:]*)$`)
)

// ParseOptions widens ParseRepository for callers that own the risk.
type ParseOptions struct {
	// AllowLocal accepts file:// URLs. Off by default: a relay-chosen file://
	// URL would let the mediator copy any repository on this machine into an
	// allowed root. Tests and the reading of a checkout's own remotes turn it on.
	AllowLocal bool
}

// ParseRepository accepts "owner/name" (GitHub), an https:// URL, an scp-like
// "git@host:owner/name.git" or an ssh:// URL. Plain-text (http, git) and
// local (file) transports are refused; see ParseOptions.
func ParseRepository(input string) (RepoRef, error) {
	return ParseRepositoryWith(input, ParseOptions{})
}

// ParseRepositoryWith is ParseRepository with options.
func ParseRepositoryWith(input string, opts ParseOptions) (RepoRef, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return RepoRef{}, errors.New("empty repository reference")
	}
	if shorthandRe.MatchString(in) {
		name := strings.TrimSuffix(path.Base(in), ".git")
		return RepoRef{Input: input, URL: "https://github.com/" + strings.TrimSuffix(in, ".git"), Name: name}, nil
	}
	if strings.Contains(in, "://") {
		u, err := url.Parse(in)
		if err != nil {
			return RepoRef{}, fmt.Errorf("invalid repository URL: %w", err)
		}
		switch u.Scheme {
		case "https", "ssh":
		case "file":
			if !opts.AllowLocal {
				return RepoRef{}, errors.New("local (file://) repositories are not accepted from the relay")
			}
		default:
			return RepoRef{}, fmt.Errorf("unsupported repository URL scheme %q (https or ssh only)", u.Scheme)
		}
		name := repoNameFromPath(u.Path)
		if name == "" {
			return RepoRef{}, errors.New("repository URL has no path")
		}
		return RepoRef{Input: input, URL: in, Name: name}, nil
	}
	if m := scpLikeRe.FindStringSubmatch(in); m != nil {
		name := repoNameFromPath(m[2])
		if name == "" {
			return RepoRef{}, errors.New("repository reference has no path")
		}
		return RepoRef{Input: input, URL: in, Name: name}, nil
	}
	return RepoRef{}, fmt.Errorf("unrecognised repository reference %q", input)
}

func repoNameFromPath(p string) string {
	p = strings.TrimRight(p, "/")
	base := path.Base(strings.ReplaceAll(p, "\\", "/"))
	base = strings.TrimSuffix(base, ".git")
	if base == "." || base == "/" {
		return ""
	}
	return base
}

// SameRepository reports whether two references name the same repository:
// scheme and user ignored, host case-insensitive, trailing ".git" and "/"
// stripped.
func SameRepository(a, b string) bool {
	ka, oka := repoKey(a)
	kb, okb := repoKey(b)
	return oka && okb && ka == kb
}

func repoKey(ref string) (string, bool) {
	in := strings.TrimSpace(ref)
	if in == "" {
		return "", false
	}
	var host, p string
	switch {
	case shorthandRe.MatchString(in):
		host, p = "github.com", in
	case strings.Contains(in, "://"):
		u, err := url.Parse(in)
		if err != nil {
			return "", false
		}
		host, p = u.Hostname(), u.Path
	default:
		m := scpLikeRe.FindStringSubmatch(in)
		if m == nil {
			return "", false
		}
		host, p = m[1], m[2]
	}
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "", false
	}
	return strings.ToLower(host) + "/" + p, true
}
