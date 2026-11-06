package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestPolicy(t *testing.T) (*Policy, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := NewPolicy([]string{root}, root)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return p, root
}

func TestNewPolicyRequiresExistingRoots(t *testing.T) {
	if _, err := NewPolicy([]string{filepath.Join(t.TempDir(), "missing")}, ""); err == nil {
		t.Error("a missing root must be refused")
	}
	if _, err := NewPolicy(nil, ""); err == nil {
		t.Error("no roots must be refused")
	}
	root := t.TempDir()
	other := t.TempDir()
	if _, err := NewPolicy([]string{root}, other); err == nil {
		t.Error("a default directory outside every root must be refused")
	}
	p, err := NewPolicy([]string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultRoot != p.Roots[0] {
		t.Errorf("default root should fall back to the first root: %q vs %q", p.DefaultRoot, p.Roots[0])
	}
}

func TestResolveInsideRoot(t *testing.T) {
	p, root := newTestPolicy(t)
	canonRoot, _ := filepath.EvalSymlinks(root)
	got, err := p.Resolve(filepath.Join(root, "proj", "sub"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != filepath.Join(canonRoot, "proj", "sub") {
		t.Errorf("got %q, want %q", got, filepath.Join(canonRoot, "proj", "sub"))
	}
	got, err = p.Resolve(root)
	if err != nil || got != canonRoot {
		t.Errorf("the root itself must resolve to itself: %q %v", got, err)
	}
}

func TestResolveOutsideRootIsNotAllowed(t *testing.T) {
	p, _ := newTestPolicy(t)
	_, err := p.Resolve(filepath.Join(t.TempDir(), "elsewhere"))
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
	// A sibling whose name merely starts with the root name is outside too.
	_, err = p.Resolve(p.Roots[0] + "2")
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("prefix sibling: err = %v, want ErrNotAllowed", err)
	}
}

func TestResolveSymlinkEscapeIsRefused(t *testing.T) {
	p, root := newTestPolicy(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := p.Resolve(filepath.Join(root, "link", "x"))
	if !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("err = %v, want ErrEscapesRoot", err)
	}
	_, err = p.Resolve(filepath.Join(root, "link"))
	if !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("link itself: err = %v, want ErrEscapesRoot", err)
	}
}

func TestResolveSymlinkIntoRootIsAllowed(t *testing.T) {
	p, root := newTestPolicy(t)
	inside := filepath.Join(root, "inside")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(inside, link); err != nil {
		t.Fatal(err)
	}
	got, err := p.Resolve(link)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	canon, _ := filepath.EvalSymlinks(inside)
	if got != canon {
		t.Errorf("got %q, want %q", got, canon)
	}
}

// A symbolic link whose target does not exist cannot be canonicalised; the
// policy refuses it rather than treating the link as a plain name under the
// root (git and MkdirAll would then fail with EEXIST on the link).
func TestResolveDanglingSymlinkIsRefused(t *testing.T) {
	p, root := newTestPolicy(t)
	if err := os.Symlink(filepath.Join(t.TempDir(), "nonexistent"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := p.Resolve(filepath.Join(root, "link", "foo"))
	if !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("root/link/foo: err = %v, want ErrEscapesRoot", err)
	}
	_, err = p.Resolve(filepath.Join(root, "link"))
	if !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("root/link: err = %v, want ErrEscapesRoot", err)
	}
	// A suffix that merely does not exist yet is still allowed.
	if _, err := p.Resolve(filepath.Join(root, "missing", "deeper")); err != nil {
		t.Errorf("root/missing/deeper: %v", err)
	}
}

func TestResolveDotDotIsRefused(t *testing.T) {
	p, root := newTestPolicy(t)
	_, err := p.Resolve(filepath.Join(root, "..", "other"))
	if err == nil {
		t.Fatal("root/../other must be refused")
	}
	if !errors.Is(err, ErrNotAllowed) {
		t.Errorf("err = %v, want ErrNotAllowed", err)
	}
	got, err := p.Resolve(filepath.Join(root, "a", "..", "b"))
	if err != nil {
		t.Fatalf("root/a/../b stays inside: %v", err)
	}
	if filepath.Base(got) != "b" {
		t.Errorf("got %q", got)
	}
}

func TestParseRepositoryForms(t *testing.T) {
	cases := []struct {
		in, url, name string
	}{
		{"plmercereau/wrekin", "https://github.com/plmercereau/wrekin", "wrekin"},
		{"https://github.com/plmercereau/wrekin", "https://github.com/plmercereau/wrekin", "wrekin"},
		{"https://github.com/plmercereau/wrekin.git", "https://github.com/plmercereau/wrekin.git", "wrekin"},
		{"git@github.com:plmercereau/wrekin.git", "git@github.com:plmercereau/wrekin.git", "wrekin"},
		{"ssh://git@github.com/plmercereau/wrekin.git", "ssh://git@github.com/plmercereau/wrekin.git", "wrekin"},
	}
	for _, c := range cases {
		ref, err := ParseRepository(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if ref.Input != c.in || ref.URL != c.url || ref.Name != c.name {
			t.Errorf("%q: got %+v, want url %q name %q", c.in, ref, c.url, c.name)
		}
	}
	for _, bad := range []string{"", "wrekin", "/absolute/path", "owner/name/extra", "http://[::1"} {
		if _, err := ParseRepository(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

// git itself refuses a "strange hostname" starting with "-", but the parser
// must not forward a user or host that could be read as an option.
func TestParseRepositoryRefusesDashLeadingUserAndHost(t *testing.T) {
	for _, bad := range []string{"user@-evil:repo", "-user@host:repo", "-evil:repo", "git@host:-flag/repo"} {
		if _, err := ParseRepository(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	for _, ok := range []string{"git@github.com:a/b.git", "my.user_1@git.example-host.com:org/repo"} {
		if _, err := ParseRepository(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
}

// Only https and ssh reach git from a relay request: file:// would copy any
// local repository into an allowed root, http:// and git:// are plain text.
func TestParseRepositoryRefusesLocalAndPlainTextSchemes(t *testing.T) {
	for _, bad := range []string{"file:///srv/git/x.git", "file:///home/alice/.password-store", "http://github.com/a/b.git", "git://github.com/a/b.git", "ext::sh -c id"} {
		if _, err := ParseRepository(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	ref, err := ParseRepositoryWith("file:///srv/git/x.git", ParseOptions{AllowLocal: true})
	if err != nil || ref.Name != "x" {
		t.Errorf("file:// with AllowLocal: %+v %v", ref, err)
	}
	if _, err := ParseRepositoryWith("http://github.com/a/b.git", ParseOptions{AllowLocal: true}); err == nil {
		t.Error("AllowLocal must not admit http://")
	}
}

func TestSameRepositoryNormalises(t *testing.T) {
	same := [][2]string{
		{"git@github.com:a/b.git", "https://github.com/a/b"},
		{"ssh://git@GitHub.com/a/b.git", "https://github.com/a/b/"},
		{"a/b", "https://github.com/a/b.git"},
		{"file:///srv/git/x.git", "file:///srv/git/x"},
	}
	for _, s := range same {
		if !SameRepository(s[0], s[1]) {
			t.Errorf("%q and %q should be the same repository", s[0], s[1])
		}
	}
	different := [][2]string{
		{"https://github.com/a/b", "https://github.com/a/c"},
		{"https://github.com/a/b", "https://gitlab.com/a/b"},
		{"", "https://github.com/a/b"},
	}
	for _, d := range different {
		if SameRepository(d[0], d[1]) {
			t.Errorf("%q and %q should differ", d[0], d[1])
		}
	}
}

func TestProposeClonePathAvoidsCollision(t *testing.T) {
	p, _ := newTestPolicy(t)
	first := p.ProposeClonePath("wrekin")
	if first != filepath.Join(p.DefaultRoot, "wrekin") {
		t.Fatalf("first proposal = %q", first)
	}
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	second := p.ProposeClonePath("wrekin")
	if second != filepath.Join(p.DefaultRoot, "wrekin-2") {
		t.Errorf("second proposal = %q, want wrekin-2", second)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if third := p.ProposeClonePath("wrekin"); third != filepath.Join(p.DefaultRoot, "wrekin-3") {
		t.Errorf("third proposal = %q, want wrekin-3", third)
	}
	if got := p.ProposeClonePath("../evil"); filepath.Dir(got) != p.DefaultRoot {
		t.Errorf("a repository name must never leave the default root: %q", got)
	}
}

// The directory name comes from the relay-chosen repository path, percent
// decoded: it must not start with "-" (an option to a later tool) and may hold
// only a conservative character set.
func TestProposeClonePathSanitisesHostileNames(t *testing.T) {
	p, _ := newTestPolicy(t)
	cases := map[string]string{
		"-rf":           "rf",
		"a\nb":          "ab",
		"wrekin.git":    "wrekin",
		".hidden":       "hidden",
		"--":            "repository",
		"...":           "repository",
		"":              "repository",
		"a b\tc":        "abc",
		"x/y.git":       "y",
		"my-repo_2.x":   "my-repo_2.x",
		"caf\u00e9":     "caf",
		"../../../evil": "evil",
	}
	for in, want := range cases {
		got := p.ProposeClonePath(in)
		if got != filepath.Join(p.DefaultRoot, want) {
			t.Errorf("ProposeClonePath(%q) = %q, want %q under the default root", in, got, want)
		}
		if strings.ContainsAny(filepath.Base(got), "\n\t ") || strings.HasPrefix(filepath.Base(got), "-") {
			t.Errorf("ProposeClonePath(%q) = %q is a hostile directory name", in, got)
		}
	}
}
