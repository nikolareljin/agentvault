// Package gitid names a git repository in a way that does not depend on where
// it is checked out: the normalized URL of its main remote (host/owner/repo)
// and the hashes of its root commits. Two clones of one repository on two
// machines get the same identity, so a vault exported from one can find the
// project on the other.
package gitid

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNotRepo means no .git was found in the directory or any parent.
var ErrNotRepo = errors.New("not inside a git repository")

// Repo is the identity of one checkout.
type Repo struct {
	// Root is the top of the working tree (the directory holding .git).
	Root string
	// GitDir is the repository directory for this checkout; for a worktree
	// or submodule it is not Root/.git.
	GitDir string
	// CommonDir holds config and refs; it differs from GitDir for a linked
	// worktree.
	CommonDir string
	// RemoteName and Remote are the chosen remote and its normalized URL
	// (host/owner/repo). Both are empty when no remote has a network URL.
	RemoteName string
	Remote     string
	// RootCommits are the hashes of the commits with no parent reachable
	// from HEAD, sorted. Empty when HEAD has no commits yet.
	RootCommits []string
}

// defaultPorts are left out of a normalized remote: ssh://h:22/x and
// git@h:x are the same repository.
var defaultPorts = map[string]string{
	"ssh":     "22",
	"git+ssh": "22",
	"ssh+git": "22",
	"https":   "443",
	"http":    "80",
	"git":     "9418",
}

// NormalizeRemote returns host[:port]/path for a network git URL, with the
// scheme, user, password or token, default port, query, trailing slash and
// .git suffix removed and the host lower-cased. The path keeps its case and
// all its segments, so a GitLab subgroup stays in it. It returns false for a
// local path, a file:// URL, or anything without a host and an owner/repo
// path.
func NormalizeRemote(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	var host, port, path string
	if i := strings.Index(s, "://"); i > 0 {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		scheme := strings.ToLower(u.Scheme)
		if _, ok := defaultPorts[scheme]; !ok {
			return "", false // file://, or a transport we do not know
		}
		host, port, path = u.Hostname(), u.Port(), u.Path
		if port == defaultPorts[scheme] {
			port = ""
		}
	} else {
		// scp-like: [user@]host:path. A colon after a slash, or a single
		// letter before it (C:\x), is a local path.
		colon := strings.IndexByte(s, ':')
		if colon <= 0 || strings.ContainsAny(s[:colon], `/\`) {
			return "", false
		}
		hostPart := s[:colon]
		if at := strings.LastIndexByte(hostPart, '@'); at >= 0 {
			hostPart = hostPart[at+1:]
		}
		if len(hostPart) == 1 {
			return "", false
		}
		host, path = hostPart, s[colon+1:]
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" {
		return "", false
	}
	var segs []string
	for _, seg := range strings.Split(strings.ReplaceAll(path, `\`, "/"), "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", false
		}
		segs = append(segs, seg)
	}
	if n := len(segs); n > 0 {
		segs[n-1] = strings.TrimSuffix(segs[n-1], ".git")
		if segs[n-1] == "" {
			segs = segs[:n-1]
		}
	}
	if len(segs) < 2 {
		return "", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6, so a port stays unambiguous
	}
	if port != "" {
		host += ":" + port
	}
	return host + "/" + strings.Join(segs, "/"), true
}

// Runner runs git against one repository directory and returns its stdout.
// Tests replace it.
type Runner func(ctx context.Context, gitDir string, args ...string) ([]byte, error)

// gitTimeout bounds one git call; a hung git must not hang an export.
const gitTimeout = 5 * time.Second

// ExecGit runs the git binary with a timeout. It names the repository with
// --git-dir so git does not search parents, disables the fsmonitor hook a
// repository's config could name, and never prompts.
func ExecGit(ctx context.Context, gitDir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	full := append([]string{"--git-dir=" + gitDir, "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(cleanEnv(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// cleanEnv is the environment without GIT_* variables, so a caller running
// inside a git hook (GIT_DIR, GIT_OBJECT_DIRECTORY) cannot redirect the call.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

// Identify finds the repository that holds dir and returns its identity.
// The remote is read from the config file; only the root commits need git.
func Identify(ctx context.Context, dir string, run Runner) (Repo, error) {
	if run == nil {
		run = ExecGit
	}
	root, gitDir, err := findGitDir(dir)
	if err != nil {
		return Repo{}, err
	}
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(b))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		common = filepath.Clean(c)
	}
	r := Repo{Root: root, GitDir: gitDir, CommonDir: common}
	if b, err := os.ReadFile(filepath.Join(common, "config")); err == nil {
		r.RemoteName, r.Remote = pickRemote(parseRemotes(b))
	} else if !errors.Is(err, os.ErrNotExist) {
		return Repo{}, err
	}
	born, err := headBorn(gitDir, common)
	if err != nil {
		return Repo{}, err
	}
	if !born {
		return r, nil
	}
	out, err := run(ctx, gitDir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return Repo{}, err
	}
	for _, line := range strings.Fields(string(out)) {
		if !isHash(line) {
			return Repo{}, fmt.Errorf("git rev-list: unexpected output %q", line)
		}
		r.RootCommits = append(r.RootCommits, line)
	}
	sort.Strings(r.RootCommits)
	return r, nil
}

// findGitDir walks up from dir to the first directory holding .git and
// returns that directory and the repository directory .git names. A .git
// file (linked worktree, submodule) holds "gitdir: PATH".
func findGitDir(dir string) (string, string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	for cur := abs; ; {
		p := filepath.Join(cur, ".git")
		info, err := os.Stat(p)
		switch {
		case err == nil && info.IsDir():
			return cur, p, nil
		case err == nil:
			b, err := os.ReadFile(p)
			if err != nil {
				return "", "", err
			}
			line := strings.TrimSpace(string(b))
			target, ok := strings.CutPrefix(line, "gitdir:")
			target = strings.TrimSpace(target)
			if !ok || target == "" {
				return "", "", fmt.Errorf("%s: not a gitdir file", p)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(cur, target)
			}
			return cur, filepath.Clean(target), nil
		case !errors.Is(err, os.ErrNotExist):
			return "", "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", ErrNotRepo
		}
		cur = parent
	}
}

// headBorn reports whether HEAD names a commit: a detached hash, or a branch
// that exists as a loose or packed ref.
func headBorn(gitDir, common string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return false, err
	}
	head := strings.TrimSpace(string(b))
	ref, ok := strings.CutPrefix(head, "ref:")
	if !ok {
		return isHash(head), nil
	}
	ref = strings.TrimSpace(ref)
	for _, d := range []string{gitDir, common} {
		if _, err := os.Stat(filepath.Join(d, filepath.FromSlash(ref))); err == nil {
			return true, nil
		}
	}
	packed, err := os.ReadFile(filepath.Join(common, "packed-refs"))
	if err != nil {
		return false, nil
	}
	for _, line := range strings.Split(string(packed), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ref && isHash(f[0]) {
			return true, nil
		}
	}
	return false, nil
}

func isHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// parseRemotes reads the first url of each [remote "NAME"] section of a git
// config file. Section and key names are case-insensitive; the remote name
// is not.
func parseRemotes(b []byte) map[string]string {
	out := map[string]string{}
	var cur string
	in := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			in = false
			end := strings.IndexByte(line, ']')
			if end < 0 {
				continue
			}
			sec := strings.TrimSpace(line[1:end])
			name, sub, ok := strings.Cut(sec, " ")
			sub = strings.TrimSpace(sub)
			if ok && strings.EqualFold(name, "remote") && len(sub) >= 2 && sub[0] == '"' && sub[len(sub)-1] == '"' {
				cur, in = sub[1:len(sub)-1], true
			}
			continue
		}
		if !in {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		if _, seen := out[cur]; !seen {
			out[cur] = unquote(strings.TrimSpace(val))
		}
	}
	return out
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	if i := strings.IndexAny(v, "#;"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// pickRemote prefers origin, then upstream, then the other remotes by name,
// and skips any whose URL is not a network URL.
func pickRemote(remotes map[string]string) (string, string) {
	names := make([]string, 0, len(remotes))
	for n := range remotes {
		if n != "origin" && n != "upstream" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range append([]string{"origin", "upstream"}, names...) {
		if u, ok := remotes[n]; ok {
			if norm, ok := NormalizeRemote(u); ok {
				return n, norm
			}
		}
	}
	return "", ""
}
