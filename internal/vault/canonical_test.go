package vault

import "testing"

// A vault written before Azure DevOps remotes had one identity stored them
// as typed; on open they must match the repository again.
func TestDecodeVaultDataCanonicalizesAzureIdentities(t *testing.T) {
	plain := []byte(`{
		"agents": [],
		"shared": {"instructions": [
			{"name": "notes/a.md", "filename": "notes/a.md", "content": "x",
			 "source_project": "remote:dev.azure.com/org/project/_git/repo#docs"}
		]},
		"locations": {"projects": [
			{"id": "p1", "abs": "/w/repo", "git_remote": "org.visualstudio.com/project/_git/repo"}
		]}
	}`)
	vd, err := decodeVaultData(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got := vd.Locations.Projects[0].GitRemote; got != "dev.azure.com/org/project/repo" {
		t.Errorf("project remote = %q", got)
	}
	if got := vd.Shared.Instructions[0].SourceProject; got != "remote:dev.azure.com/org/project/repo#docs" {
		t.Errorf("source project = %q", got)
	}
}
