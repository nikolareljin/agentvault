package gitid

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each of these either leaks a non-host into the identity or lets two
// different repositories share one identity; all must be refused.
func TestNormalizeRemoteRejectsHostileForms(t *testing.T) {
	for _, in := range []string{
		// Encoded or backslash separators: a%2Fb/c would collide with a/b/c.
		"https://h.example/a%2Fb/c",
		"https://h.example/a%2fb/c",
		"https://h.example/a%5Cb/c",
		`https://h.example/a\b/c`,
		`h.example:a\b/c`,
		// Option-looking or malformed hosts.
		"ssh://-oProxyCommand=x/o/r",
		"-oProxyCommand=x:o/r",
		"-u:o/r",
		"ext::ssh -c touch h/o/r",
		"fd::17/o/r",
		"h_st/x:o/r",
		"ho st:o/r",
		// Control characters anywhere.
		"git@github.com:owner/re\npo",
		"git@github.com:owner/re\x00po",
		"https://github.com/owner/re%0Apo",
		"gith\tub.com:owner/repo",
		"git@github.com:owner/re\u009bpo",
		// Non-ASCII hosts: homoglyphs and IDN (one repo, two spellings).
		"https://g\u0456thub.com/owner/repo",
		"https://b\u00fccher.example/owner/repo",
		"git@b\u00fccher.example:owner/repo",
		// Not an IP in brackets.
		"ssh://[not:an:ip]/owner/repo",
	} {
		if got, ok := NormalizeRemote(in); ok {
			t.Errorf("NormalizeRemote(%q) = %q, want refused", in, got)
		}
	}
	// Still accepted.
	for in, want := range map[string]string{
		"https://xn--bcher-kva.example/owner/repo": "xn--bcher-kva.example/owner/repo",
		"git@my_host.example:owner/repo":           "my_host.example/owner/repo",
		"https://h.example/owner/re%20po":          "h.example/owner/re po",
	} {
		if got, ok := NormalizeRemote(in); !ok || got != want {
			t.Errorf("NormalizeRemote(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// git follows include and includeIf; the owner check covers only the files
// it can name, so an include must skip git.
func TestIdentifySkipsGitWhenConfigIncludes(t *testing.T) {
	needGit(t)
	for _, sec := range []string{"[include]\n\tpath = /elsewhere\n", "[includeIf \"gitdir:/x/\"]\n\tpath = y\n", "[Include]\n\tpath = z\n"} {
		dir, _ := newRepo(t, "")
		f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(sec); err != nil {
			t.Fatal(err)
		}
		f.Close()
		called := false
		r, err := Identify(context.Background(), dir, func(context.Context, string, string, ...string) ([]byte, error) {
			called = true
			return nil, nil
		})
		if err != nil || called || r.RootSkipped == "" {
			t.Errorf("%q: %+v %v called=%v", sec, r, err, called)
		}
	}
}

func TestIdentifySkipsGitWhenWorktreeConfigIncludes(t *testing.T) {
	needGit(t)
	dir, _ := newRepo(t, "")
	if err := os.WriteFile(filepath.Join(dir, ".git", "config.worktree"), []byte("[include]\npath=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	r, err := Identify(context.Background(), dir, func(context.Context, string, string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err != nil || called || r.RootSkipped == "" {
		t.Errorf("%+v %v called=%v", r, err, called)
	}
}

func TestHasInclude(t *testing.T) {
	cases := map[string]bool{
		"[core]\n\tbare = false\n":             false,
		"[ include ]\npath = x\n":              true,
		"[includeIf \"onbranch:x\"]\n":         true,
		"[core]\n\tx = [include]\n":            false,
		strings.Repeat("a", 70000) + "\n[x]\n": true, // unscannable: assume include
	}
	for in, want := range cases {
		if got := hasInclude([]byte(in)); got != want {
			t.Errorf("hasInclude(%.40q) = %v, want %v", in, got, want)
		}
	}
}
