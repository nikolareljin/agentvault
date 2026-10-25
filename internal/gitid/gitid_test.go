package gitid

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" means not a network remote
	}{
		{"git@github.com:Owner/Repo.git", "github.com/owner/repo"},
		{"https://gitlab.com/Group/Repo", "gitlab.com/Group/Repo"},
		{"git@git.example.com:Owner/Repo.git", "git.example.com/Owner/Repo"},
		{"https://BitBucket.org/Team/Repo", "bitbucket.org/team/repo"},
		{"https://GitHub.com./Owner/Repo", "github.com/owner/repo"},
		{"git@github.com.:owner/repo.git", "github.com/owner/repo"},
		{"https://GitHub.com../Owner/Repo", "github.com/owner/repo"},
		{"github.com:owner/repo", "github.com/owner/repo"},
		{"https://github.com/owner/repo.git", "github.com/owner/repo"},
		{"https://github.com/owner/repo", "github.com/owner/repo"},
		{"https://github.com/owner/repo/", "github.com/owner/repo"},
		{"https://github.com/owner/repo.git/", "github.com/owner/repo"},
		{"https://x-access-token:ghp_secret@github.com/owner/repo.git", "github.com/owner/repo"},
		{"https://oauth2:glpat-secret" + "@gitlab.com/g/r.git", "gitlab.com/g/r"},
		{"https://user@GitHub.COM/owner/repo", "github.com/owner/repo"},
		{"git@GITHUB.com:owner/repo.git", "github.com/owner/repo"},
		{"ssh://git@github.com/owner/repo.git", "github.com/owner/repo"},
		{"ssh://git@github.com:22/owner/repo.git", "github.com/owner/repo"},
		{"git+ssh://git@github.com/owner/repo", "github.com/owner/repo"},
		{"https://github.com:443/owner/repo", "github.com/owner/repo"},
		{"http://git.example.com:80/owner/repo", "git.example.com/owner/repo"},
		{"git://git.example.com:9418/owner/repo", "git.example.com/owner/repo"},
		{"https://git.example.com:8443/owner/repo.git", "git.example.com:8443/owner/repo"},
		{"ssh://git@bitbucket.example.com:7999/proj/repo.git", "bitbucket.example.com:7999/proj/repo"},
		{"git@gitlab.com:group/sub/deeper/repo.git", "gitlab.com/group/sub/deeper/repo"},
		{"https://gitlab.com/group/sub/repo", "gitlab.com/group/sub/repo"},
		{"git@bitbucket.org:team/repo.git", "bitbucket.org/team/repo"},
		{"https://user" + "@bitbucket.org/team/repo.git", "bitbucket.org/team/repo"},
		{"git.example.com:/srv/git/repo.git", "git.example.com/srv/git/repo"},
		{"ssh://[::1]:2222/owner/repo", "[::1]:2222/owner/repo"},
		{"https://github.com/owner/repo.git?x=1#frag", "github.com/owner/repo"},
		{"  git@github.com:owner/repo.git\n", "github.com/owner/repo"},
		{"https://github.com//owner//repo", "github.com/owner/repo"},
		// Credentials in scp-like form never survive.
		{"user:tok" + "@github.com:owner/repo.git", "github.com/owner/repo"},
		{"a@b:tok" + "@github.com:owner/repo", "github.com/owner/repo"},
		{"git@github.com:owner/re@po", ""},
		{"git@github.com:own:er/repo", ""},
		{"https://github.com/owner/re@po", ""},
		{"https://github.com/own:er/repo", ""},
		// Not network remotes.
		{"", ""},
		{"file:///srv/git/repo.git", ""},
		{"/srv/git/repo.git", ""},
		{"../repo.git", ""},
		{"./repo", ""},
		{`C:\repos\x.git`, ""},
		{"C:/repos/x.git", ""},
		{"ftp://example.com/owner/repo", ""},
		{"https://github.com/repo", ""},
		{"https://github.com/", ""},
		{"git@github.com:repo.git", ""},
		{"https://github.com/owner/../repo", ""},
		{"https:///owner/repo", ""},
	}
	for _, c := range cases {
		got, ok := NormalizeRemote(c.in)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("NormalizeRemote(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "@") {
			t.Errorf("NormalizeRemote(%q) kept credentials: %q", c.in, got)
		}
	}
}

func TestParseRemotesAndPick(t *testing.T) {
	cases := []struct {
		name, config, wantName, wantURL string
	}{
		{"origin first", `[remote "upstream"]
	url = git@github.com:up/repo.git
[remote "origin"]
	url = git@github.com:me/repo.git
`, "origin", "github.com/me/repo"},
		{"upstream when origin is local", `[remote "origin"]
	url = /srv/mirror/repo.git
[remote "upstream"]
	url = https://github.com/up/repo
`, "upstream", "github.com/up/repo"},
		{"other remotes by name", `[remote "zeta"]
	url = https://github.com/z/repo
[remote "alpha"]
	url = https://github.com/a/repo
`, "alpha", "github.com/a/repo"},
		{"case-insensitive section and key, quoted value", `[REMOTE "origin"]
	URL = "https://github.com/q/repo.git"
`, "origin", "github.com/q/repo"},
		{"first url wins, comments skipped", `# comment
[remote "origin"]
	; note
	url = https://github.com/first/repo # trailing
	url = https://github.com/second/repo
`, "origin", "github.com/first/repo"},
		{"remote name is case-sensitive", `[remote "Origin"]
	url = https://github.com/cap/repo
[remote "b"]
	url = https://github.com/b/repo
`, "Origin", "github.com/cap/repo"},
		{"no remotes", "[core]\n\tbare = false\n", "", ""},
		{"only local", `[remote "origin"]
	url = file:///srv/repo
`, "", ""},
	}
	for _, c := range cases {
		n, u := pickRemote(parseRemotes([]byte(c.config)))
		if n != c.wantName || u != c.wantURL {
			t.Errorf("%s: got %q %q, want %q %q", c.name, n, u, c.wantName, c.wantURL)
		}
	}
}

// git runs the real git binary with no global or system config, so the test
// never reads or writes the user's.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "init.defaultBranch=main", "-c", "protocol.file.allow=always", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func newRepo(t *testing.T, remote string) (string, string) {
	t.Helper()
	trustOwnerOnWindows(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if remote != "" {
		git(t, dir, "remote", "add", "origin", remote)
	}
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "root")
	root := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "second")
	return dir, root
}

func TestIdentifyRepoAndSubdir(t *testing.T) {
	needGit(t)
	dir, root := newRepo(t, "git@github.com:Owner/Repo.git")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dir, sub} {
		r, err := Identify(context.Background(), d, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Remote != "github.com/owner/repo" || r.RemoteName != "origin" {
			t.Errorf("%s: remote %q %q", d, r.RemoteName, r.Remote)
		}
		if len(r.RootCommits) != 1 || r.RootCommits[0] != root {
			t.Errorf("%s: roots %v, want [%s]", d, r.RootCommits, root)
		}
		if !sameFile(r.Root, dir) {
			t.Errorf("%s: root %q, want %q", d, r.Root, dir)
		}
	}
}

func TestIdentifyNoCommitsDoesNotRunGit(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	called := false
	r, err := Identify(context.Background(), dir, func(context.Context, string, string, ...string) ([]byte, error) {
		called = true
		return nil, errors.New("must not run")
	})
	if err != nil || called || len(r.RootCommits) != 0 || r.Remote != "" {
		t.Fatalf("got %+v, %v, called=%v", r, err, called)
	}
}

func TestIdentifyLocalOnlyRepoHasRootButNoRemote(t *testing.T) {
	needGit(t)
	dir, root := newRepo(t, "/srv/git/x.git")
	r, err := Identify(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Remote != "" || len(r.RootCommits) != 1 || r.RootCommits[0] != root {
		t.Fatalf("got %+v", r)
	}
}

func TestIdentifyWorktree(t *testing.T) {
	needGit(t)
	dir, root := newRepo(t, "https://tok"+"@gitlab.com/g/sub/r.git")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	r, err := Identify(context.Background(), wt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sameFile(r.Root, wt) || r.Remote != "gitlab.com/g/sub/r" || len(r.RootCommits) != 1 || r.RootCommits[0] != root {
		t.Fatalf("got %+v", r)
	}
	if !sameFile(r.CommonDir, filepath.Join(dir, ".git")) || sameFile(r.GitDir, r.CommonDir) {
		t.Fatalf("gitdir %q commondir %q", r.GitDir, r.CommonDir)
	}
}

func TestIdentifySubmodule(t *testing.T) {
	needGit(t)
	lib, libRoot := newRepo(t, "git@github.com:o/lib.git")
	super, _ := newRepo(t, "git@github.com:o/super.git")
	git(t, super, "submodule", "add", "-q", lib, "vendor/lib")
	// The submodule's own origin is the local path it was cloned from; give
	// it a network one the way a real clone would have.
	git(t, filepath.Join(super, "vendor", "lib"), "remote", "set-url", "origin", "git@github.com:o/lib.git")
	r, err := Identify(context.Background(), filepath.Join(super, "vendor", "lib"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Remote != "github.com/o/lib" || len(r.RootCommits) != 1 || r.RootCommits[0] != libRoot {
		t.Fatalf("got %+v", r)
	}
	if !strings.Contains(r.GitDir, filepath.Join(".git", "modules")) {
		t.Fatalf("gitdir %q", r.GitDir)
	}
}

func TestIdentifyMultipleRoots(t *testing.T) {
	needGit(t)
	dir, root1 := newRepo(t, "")
	git(t, dir, "checkout", "-q", "--orphan", "other")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "other root")
	root2 := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "merge", "-q", "--allow-unrelated-histories", "-m", "join", "main")
	r, err := Identify(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root1, root2}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if len(r.RootCommits) != 2 || r.RootCommits[0] != want[0] || r.RootCommits[1] != want[1] {
		t.Fatalf("roots %v, want %v", r.RootCommits, want)
	}
}

func TestIdentifyPackedRefAndDetachedHead(t *testing.T) {
	needGit(t)
	dir, root := newRepo(t, "")
	git(t, dir, "pack-refs", "--all")
	if _, err := os.Stat(filepath.Join(dir, ".git", "refs", "heads", "main")); err == nil {
		t.Fatal("ref still loose after pack-refs")
	}
	r, err := Identify(context.Background(), dir, nil)
	if err != nil || len(r.RootCommits) != 1 || r.RootCommits[0] != root {
		t.Fatalf("packed: %+v %v", r, err)
	}
	git(t, dir, "checkout", "-q", "--detach")
	r, err = Identify(context.Background(), dir, nil)
	if err != nil || len(r.RootCommits) != 1 || r.RootCommits[0] != root {
		t.Fatalf("detached: %+v %v", r, err)
	}
}

func sameFile(a, b string) bool {
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}

func TestIdentifyRejectsHeadRefOutsideRefs(t *testing.T) {
	needGit(t)
	dir, _ := newRepo(t, "")
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: ../../../../etc/passwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	r, err := Identify(context.Background(), dir, func(context.Context, string, string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err != nil || called || len(r.RootCommits) != 0 {
		t.Fatalf("got %+v %v called=%v", r, err, called)
	}
}

func TestExecGitHardening(t *testing.T) {
	args, env := gitInvocation("/x/.git", "/x/common", "rev-list")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--git-dir=/x/.git", "core.hooksPath=" + os.DevNull, "core.fsmonitor=false", "protocol.allow=never", "remote.origin.promisor=false"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q lack %q", joined, want)
		}
	}
	envs := strings.Join(env, "\n")
	for _, want := range []string{"GIT_NO_LAZY_FETCH=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_COMMON_DIR=/x/common"} {
		if !strings.Contains(envs, want) {
			t.Errorf("env lacks %q", want)
		}
	}
}

func TestIdentifyNotRepo(t *testing.T) {
	_, err := Identify(context.Background(), t.TempDir(), nil)
	if !errors.Is(err, ErrNotRepo) {
		t.Fatalf("got %v, want ErrNotRepo", err)
	}
}

func TestIdentifyBadGitFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Identify(context.Background(), dir, nil); err == nil {
		t.Fatal("want error for a .git file without gitdir:")
	}
}

func TestIdentifyRejectsBadRunnerOutput(t *testing.T) {
	needGit(t)
	dir, _ := newRepo(t, "")
	_, err := Identify(context.Background(), dir, func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte("not-a-hash\n"), nil
	})
	if err == nil {
		t.Fatal("want error for non-hash output")
	}
}

func TestIdentifyRunnerErrorPropagates(t *testing.T) {
	needGit(t)
	dir, _ := newRepo(t, "")
	_, err := Identify(context.Background(), dir, func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}

// trustOwnerOnWindows lets the tests that read root commits run on Windows,
// where the real owner check fails closed.
func trustOwnerOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		return
	}
	old, oldRemoteOnly := ownerCheck, remoteOnly
	ownerCheck = func(string) bool { return true }
	remoteOnly = false
	t.Cleanup(func() { ownerCheck, remoteOnly = old, oldRemoteOnly })
}

func TestParseCommonDir(t *testing.T) {
	g := filepath.Join(string(filepath.Separator)+"r", ".git", "worktrees", "w")
	cases := []struct{ content, want string }{
		{"../..\n", filepath.Join(g, "..", "..")},
		{"../..\r\n", filepath.Join(g, "..", "..")},
		{"../.. \n", filepath.Join(g, "..", ".. ")},
		{" x\n", filepath.Join(g, " x")},
	}
	for _, c := range cases {
		if got := parseCommonDir(g, []byte(c.content)); got != c.want {
			t.Errorf("parseCommonDir(%q) = %q, want %q", c.content, got, c.want)
		}
	}
}

func TestIdentifyChecksOwnerOfEveryPathGitLoads(t *testing.T) {
	needGit(t)
	dir, _ := newRepo(t, "")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	git(t, wt, "config", "extensions.worktreeConfig", "true")
	git(t, wt, "config", "--worktree", "x.y", "z")
	var seen []string
	old := ownerCheck
	t.Cleanup(func() { ownerCheck = old })
	ownerCheck = func(p string) bool { seen = append(seen, p); return true }
	r, err := Identify(context.Background(), wt, nil)
	if err != nil || len(r.RootCommits) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	for _, want := range []string{r.Root, r.GitDir, r.CommonDir, filepath.Join(r.CommonDir, "config"), filepath.Join(r.GitDir, "config.worktree")} {
		found := false
		for _, p := range seen {
			found = found || p == want
		}
		if !found {
			t.Errorf("owner of %q not checked; checked %v", want, seen)
		}
	}
	// Any one foreign path skips git.
	for _, bad := range []string{filepath.Join(r.CommonDir, "config"), filepath.Join(r.GitDir, "config.worktree"), r.Root} {
		ownerCheck = func(p string) bool { return p != bad }
		called := false
		r2, err := Identify(context.Background(), wt, func(context.Context, string, string, ...string) ([]byte, error) {
			called = true
			return nil, nil
		})
		if err != nil || called || r2.RootSkipped == "" || len(r2.RootCommits) != 0 {
			t.Errorf("foreign %s: %+v %v called=%v", bad, r2, err, called)
		}
	}
}

func TestNormalizeRemoteAzureDevOps(t *testing.T) {
	const want = "dev.azure.com/org/project/repo"
	same := []string{
		"https://dev.azure.com/org/project/_git/repo",
		"https://org" + "@dev.azure.com/org/project/_git/repo",
		"https://user:pat" + "@dev.azure.com/org/project/_git/repo.git",
		"https://dev.azure.com/Org/Project/_git/Repo/",
		"https://dev.azure.com:443/org/project/_git/repo",
		"git@ssh.dev.azure.com:v3/org/project/repo",
		"git@ssh.dev.azure.com:v3/Org/Project/Repo.git",
		"ssh://git@ssh.dev.azure.com/v3/org/project/repo",
		"ssh://git@ssh.dev.azure.com:22/v3/org/project/repo",
		"org" + "@vs-ssh.visualstudio.com:v3/org/project/repo",
		"ssh://org" + "@vs-ssh.visualstudio.com/v3/org/project/repo",
		"https://org.visualstudio.com/project/_git/repo",
		"https://user:pat" + "@org.visualstudio.com/project/_git/repo.git",
		"https://ORG.visualstudio.com/DefaultCollection/project/_git/repo",
	}
	for _, in := range same {
		got, ok := NormalizeRemote(in)
		if !ok || got != want {
			t.Errorf("NormalizeRemote(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}

	other := []struct{ in, want string }{
		// Two repositories in one project stay apart.
		{"https://dev.azure.com/org/project/_git/other", "dev.azure.com/org/project/other"},
		// A repository named like its project may leave the project out.
		{"https://dev.azure.com/org/_git/project", "dev.azure.com/org/project/project"},
		{"https://org.visualstudio.com/_git/project", "dev.azure.com/org/project/project"},
		{"https://dev.azure.com/org/project/_git/project", "dev.azure.com/org/project/project"},
		// A space in a project name: decoded in https, encoded in scp-like.
		{"https://dev.azure.com/org/My%20Project/_git/repo", "dev.azure.com/org/my project/repo"},
		{"git@ssh.dev.azure.com:v3/org/My%20Project/repo", "dev.azure.com/org/my project/repo"},
		// Paths that match no Azure DevOps form normalize like any remote.
		{"https://dev.azure.com/org/project", "dev.azure.com/org/project"},
		{"https://dev.azure.com:8443/org/project/_git/repo", "dev.azure.com:8443/org/project/_git/repo"},
		{"https://a.b.visualstudio.com/project/_git/repo", "a.b.visualstudio.com/project/_git/repo"},
		{"git@ssh.dev.azure.com:v2/org/project/repo", "ssh.dev.azure.com/v2/org/project/repo"},
		// Unsafe names are refused.
		{"git@ssh.dev.azure.com:v3/org/a%2Fb/repo", ""},
		{"git@ssh.dev.azure.com:v3/org/project/%zz", ""},
		{"git@ssh.dev.azure.com:v3/org/project/a%40b", ""},
		{"git@ssh.dev.azure.com:v3/org/project/a%0Ab", ""},
		{"git@ssh.dev.azure.com:v3/org/_project/repo", ""},
		{"https://dev.azure.com/org/_git/_git/repo", ""},
	}
	for _, c := range other {
		got, ok := NormalizeRemote(c.in)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("NormalizeRemote(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
}

func TestCanonicalStored(t *testing.T) {
	cases := []struct{ in, want string }{
		// Azure DevOps remotes as earlier versions stored them.
		{"dev.azure.com/org/project/_git/repo", "dev.azure.com/org/project/repo"},
		{"dev.azure.com/Org/Project/_git/Repo", "dev.azure.com/org/project/repo"},
		{"org.visualstudio.com/project/_git/repo", "dev.azure.com/org/project/repo"},
		{"ssh.dev.azure.com/v3/org/project/repo", "dev.azure.com/org/project/repo"},
		{"vs-ssh.visualstudio.com/v3/org/project/repo", "dev.azure.com/org/project/repo"},
		{"dev.azure.com/org/my project/_git/repo", "dev.azure.com/org/my project/repo"},
		// Already canonical, or not an Azure DevOps form: unchanged.
		{"dev.azure.com/org/project/repo", "dev.azure.com/org/project/repo"},
		{"dev.azure.com/org/project", "dev.azure.com/org/project"},
		{"github.com/owner/repo", "github.com/owner/repo"},
		{"git.example.com:443/owner/repo", "git.example.com:443/owner/repo"},
		{"gitlab.com/Group/Repo", "gitlab.com/Group/Repo"},
		{"", ""},
	}
	for _, c := range cases {
		if got := CanonicalStored(c.in); got != c.want {
			t.Errorf("CanonicalStored(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
