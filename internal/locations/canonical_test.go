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
			{ID: "a", GitRemote: "dev.azure.com/org/project/_git/repo"},
			{ID: "b", GitRemote: "github.com/owner/repo"},
		},
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
	if p, ok := s.FindByIdentity("dev.azure.com/org/project/repo", nil); !ok || p.ID != "a" {
		t.Errorf("FindByIdentity after Canonicalize = %+v, %v", p, ok)
	}
}
