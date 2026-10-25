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

// One referenced file pulled from an SSH and an HTTPS clone of one Azure
// DevOps repository was two entries; after the rewrite it is one, the newer.
func TestDecodeVaultDataFoldsAzureDuplicates(t *testing.T) {
	plain := []byte(`{"agents": [], "shared": {"instructions": [
		{"name": "notes/a.md", "filename": "notes/a.md", "content": "ssh", "updated_at": "2026-10-01T00:00:00Z",
		 "source_project": "remote:ssh.dev.azure.com/v3/org/project/repo"},
		{"name": "notes/a.md", "filename": "notes/a.md", "content": "https", "updated_at": "2026-10-02T00:00:00Z",
		 "source_project": "remote:dev.azure.com/org/project/_git/repo"}
	]}}`)
	vd, err := decodeVaultData(plain)
	if err != nil {
		t.Fatal(err)
	}
	got := vd.Shared.Instructions
	if len(got) != 1 || got[0].Content != "https" || got[0].SourceProject != "remote:dev.azure.com/org/project/repo" {
		t.Errorf("instructions = %+v", got)
	}
}
