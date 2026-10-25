package cmd

import (
	"encoding/json"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/locations"
)

// A bundle exported before Azure DevOps remotes had one identity names its
// projects by the remote as typed; decoding brings them up to date.
func TestDecodePortableBundleCanonicalizesAzureIdentities(t *testing.T) {
	b := newPortableBundle(nowFunc())
	b.Profiles = []PortableProfile{{Name: "default", Setup: SetupBundle{
		SharedConfig: agent.SharedConfig{Instructions: []agent.InstructionFile{{
			Name: "notes/a.md", Filename: "notes/a.md", Content: "x",
			SourceProject: "remote:dev.azure.com/org/project/_git/repo",
		}}},
		Locations: &locations.Set{Projects: []locations.Project{{
			ID: "p1", Abs: "/w/repo", GitRemote: "dev.azure.com/Org/Project/_git/Repo",
		}}},
	}}}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := decodePortableBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	s := got.Profiles[0].Setup
	if r := s.Locations.Projects[0].GitRemote; r != "dev.azure.com/org/project/repo" {
		t.Errorf("project remote = %q", r)
	}
	if k := s.SharedConfig.Instructions[0].SourceProject; k != "remote:dev.azure.com/org/project/repo" {
		t.Errorf("source project = %q", k)
	}
}
