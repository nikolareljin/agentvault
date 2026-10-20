package gitid

import (
	"context"
	"errors"
	"testing"
)

// One repository has many spellings. Each group below must collapse to one
// identity, and no two groups may share one: two identities for one repo
// would make an import miss it, one identity for two repos would point it
// at the wrong checkout.
func TestRemoteSpellingsCollapseToOneIdentity(t *testing.T) {
	groups := map[string][]string{
		"github.com/owner/repo": {
			"git@github.com:owner/repo.git",
			"git@github.com:owner/repo",
			"git@github.com:owner/repo.git/",
			"github.com:owner/repo",
			"ssh://git@github.com/owner/repo",
			"ssh://git@github.com/owner/repo.git",
			"ssh://git@github.com:22/owner/repo",
			"ssh://git@github.com:22/owner/repo.git/",
			"ssh://github.com/owner/repo",
			"git+ssh://git@github.com/owner/repo.git",
			"https://github.com/owner/repo",
			"https://github.com/owner/repo.git",
			"https://github.com/owner/repo/",
			"https://user@github.com/owner/repo.git",
			"https://user:tok@github.com/owner/repo",
			"https://github.com:443/owner/repo.git",
			"https://GitHub.com/Owner/Repo.git",
			"git@github.com:OWNER/REPO.git",
		},
		"gitlab.com/group/sub/repo": {
			"git@gitlab.com:group/sub/repo.git",
			"ssh://git@gitlab.com:22/group/sub/repo",
			"https://gitlab.com/group/sub/repo",
			"https://oauth2:x" + "@gitlab.com/group/sub/repo.git/",
		},
		"gitlab.com/Group/sub/repo": {
			"git@gitlab.com:Group/sub/repo.git",
			"https://gitlab.com/Group/sub/repo",
		},
		"bitbucket.org/team/repo": {
			"git@bitbucket.org:Team/Repo.git",
			"ssh://git@bitbucket.org/team/repo",
			"https://user" + "@bitbucket.org/TEAM/repo.git",
		},
		"git.example.com:2222/owner/repo": {
			"ssh://git@git.example.com:2222/owner/repo.git",
		},
	}
	owner := map[string]string{}
	for want, forms := range groups {
		for _, f := range forms {
			got, ok := NormalizeRemote(f)
			if !ok || got != want {
				t.Errorf("NormalizeRemote(%q) = %q, %v; want %q", f, got, ok, want)
			}
			if prev, seen := owner[got]; seen && prev != want {
				t.Errorf("%q and group %q share identity %q", f, prev, got)
			}
			owner[got] = want
		}
	}
	if len(owner) != len(groups) {
		t.Errorf("%d groups gave %d identities: %v", len(groups), len(owner), owner)
	}
}

func TestIdentifyRemoteOnlySkipsGit(t *testing.T) {
	needGit(t)
	dir, _ := newRepo(t, "git@github.com:o/r.git")
	trustOwnerOnWindows(t)
	old := remoteOnly
	remoteOnly = true
	t.Cleanup(func() { remoteOnly = old })
	r, err := Identify(context.Background(), dir, func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("must not run")
	})
	if err != nil || r.Remote != "github.com/o/r" || len(r.RootCommits) != 0 || r.RootSkipped == "" {
		t.Fatalf("got %+v, %v", r, err)
	}
}
