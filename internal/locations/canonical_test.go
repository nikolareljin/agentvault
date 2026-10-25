package locations

import "testing"

func TestCanonicalKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"remote:dev.azure.com/org/project/_git/repo", "remote:dev.azure.com/org/project/repo"},
		{"remote:org.visualstudio.com/project/_git/repo#docs/a", "remote:dev.azure.com/org/project/repo#docs/a"},
		{"remote:github.com/owner/repo#sub", "remote:github.com/owner/repo#sub"},
		{"roots:abc", "roots:abc"},
		{"path:/home/u/dev.azure.com/org/project/_git/repo", "path:/home/u/dev.azure.com/org/project/_git/repo"},
		{"", ""},
	}
	for _, c := range cases {
		if got := CanonicalKey(c.in); got != c.want {
			t.Errorf("CanonicalKey(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSetCanonicalize(t *testing.T) {
	s := Set{
		Projects: []Project{
			{ID: "a", Abs: "/a", GitRemote: "dev.azure.com/org/project/_git/repo"},
			{ID: "b", Abs: "/b", GitRemote: "github.com/owner/repo"},
		},
		WorkDirMaps: map[string]string{"/w": "/t"},
		ConfirmedHidden: map[string]HiddenConfirm{
			"/w": {Target: "/t", Projects: []string{"remote:org.visualstudio.com/project/_git/repo", "roots:abc"}},
		},
	}
	s.Canonicalize()
	if got := s.Projects[0].GitRemote; got != "dev.azure.com/org/project/repo" {
		t.Errorf("project a remote = %q", got)
	}
	if got := s.Projects[1].GitRemote; got != "github.com/owner/repo" {
		t.Errorf("project b remote = %q", got)
	}
	got := s.ConfirmedHidden["/w"].Projects
	if got[0] != "remote:dev.azure.com/org/project/repo" || got[1] != "roots:abc" {
		t.Errorf("confirmed hidden projects = %q", got)
	}
	// A project stored before keeps matching its repository.
	if p, ok := s.FindByIdentity("dev.azure.com/org/project/repo", nil); !ok || p.Abs != "/a" {
		t.Errorf("FindByIdentity after Canonicalize = %+v, %v", p, ok)
	}
}

func TestSetCanonicalizeFoldsAzureDuplicates(t *testing.T) {
	s := Set{
		WorkDirs: []WorkDir{{ID: "w1", Abs: "/w"}},
		Projects: []Project{
			{ID: "p1", WorkDir: "w1", Abs: "/w/ssh", GitRemote: "ssh.dev.azure.com/v3/org/project/repo"},
			{ID: "p2", WorkDir: "w1", Abs: "/w/https", GitRemote: "dev.azure.com/org/project/_git/repo"},
			{ID: "p3", Abs: "/x", GitRemote: "github.com/owner/repo"},
		},
		WorkDirMaps: map[string]string{"/src": "/w"},
	}
	s.Canonicalize()
	if len(s.Projects) != 2 {
		t.Fatalf("projects = %+v", s.Projects)
	}
	p := s.Projects[0]
	if p.GitRemote != "dev.azure.com/org/project/repo" || p.Abs != "/w/https" || len(p.Aliases) != 1 || p.Aliases[0] != "/w/ssh" || p.WorkDir != "w1" {
		t.Errorf("folded project = %+v", p)
	}
	if s.Projects[1].GitRemote != "github.com/owner/repo" || s.WorkDirMaps["/src"] != "/w" {
		t.Errorf("set = %+v", s)
	}
}
