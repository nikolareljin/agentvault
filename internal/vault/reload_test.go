package vault

import (
	"errors"
	"os"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/crypto"
)

func TestReloadSeesAnotherProcessWrite(t *testing.T) {
	path := tempVaultPath(t)
	tui := New(path)
	if err := tui.Init("pass-123456789"); err != nil {
		t.Fatal(err)
	}
	cli := New(path)
	if err := cli.Unlock("pass-123456789"); err != nil {
		t.Fatal(err)
	}
	if err := cli.AddCapability(agent.ModelCapabilityEntry{EndpointURL: "http://h:11434", ModelName: "m", Capabilities: []string{"coding"}}); err != nil {
		t.Fatal(err)
	}
	if err := tui.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := tui.ListCapabilities(); len(got) != 1 {
		t.Fatalf("capabilities after reload = %v", got)
	}
	// The next save keeps the other process's entry.
	if err := tui.Add(agent.Agent{Name: "a", Provider: agent.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	again := New(path)
	if err := again.Unlock("pass-123456789"); err != nil {
		t.Fatal(err)
	}
	if len(again.ListCapabilities()) != 1 || len(again.List()) != 1 {
		t.Fatalf("on disk: caps=%v agents=%v", again.ListCapabilities(), again.List())
	}
}

func TestReloadLocksWhenHeaderChanged(t *testing.T) {
	path := tempVaultPath(t)
	tui := New(path)
	if err := tui.Init("pass-123456789"); err != nil {
		t.Fatal(err)
	}
	// Another process writes the vault under a new password.
	otherPath := tempVaultPath(t) + ".other"
	if err := New(otherPath).Init("other-pass-1234"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tui.Reload(); !errors.Is(err, ErrVaultRelocked) {
		t.Fatalf("Reload err = %v, want ErrVaultRelocked", err)
	}
	if err := tui.Add(agent.Agent{Name: "a", Provider: agent.ProviderClaude}); err == nil {
		t.Fatal("save after a relock succeeded; it would undo the password change")
	}
	check := New(path)
	if err := check.Unlock("other-pass-1234"); err != nil {
		t.Fatalf("new password no longer opens the vault: %v", err)
	}
}

func TestReloadLegacyUnchangedIsNoop(t *testing.T) {
	path := tempVaultPath(t)
	salt, _ := crypto.GenerateSalt()
	key, _ := crypto.DeriveKey("old-pass", salt)
	ct, err := crypto.Encrypt([]byte(`{"agents":[{"name":"legacy","provider":"claude"}]}`), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(salt, ct...), 0o600); err != nil {
		t.Fatal(err)
	}
	v := New(path)
	if err := v.Unlock("old-pass"); err != nil {
		t.Fatal(err)
	}
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload of an unchanged legacy file: %v", err)
	}
	if err := v.Add(agent.Agent{Name: "new", Provider: agent.ProviderCodex}); err != nil {
		t.Fatalf("save after reload: %v", err)
	}
	if err := v.Reload(); err != nil || len(v.List()) != 2 {
		t.Fatalf("Reload after upgrade: err=%v agents=%v", err, v.List())
	}
}

func TestReloadLocksWhenFileUnreadable(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("pass-123456789"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := v.Reload(); err == nil {
		t.Fatal("Reload of a missing file succeeded")
	}
	if v.Unlocked() {
		t.Fatal("vault still unlocked after a failed reload")
	}
	if err := v.Add(agent.Agent{Name: "a", Provider: agent.ProviderClaude}); err == nil {
		t.Fatal("save after a failed reload succeeded")
	}
}

func TestLockedVaultSettersChangeNothing(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("pass-123456789"); err != nil {
		t.Fatal(err)
	}
	if err := v.Add(agent.Agent{Name: "keep", Provider: agent.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_ = v.Reload() // locks
	if err := v.Remove("keep"); err == nil {
		t.Fatal("Remove on a locked vault succeeded")
	}
	if err := v.AddCapability(agent.ModelCapabilityEntry{EndpointURL: "http://h:1", ModelName: "m"}); err == nil {
		t.Fatal("AddCapability on a locked vault succeeded")
	}
	if len(v.List()) != 1 || len(v.ListCapabilities()) != 0 {
		t.Fatalf("locked vault changed in memory: agents=%v caps=%v", v.List(), v.ListCapabilities())
	}
}

func TestSnapshotIsDeepAndReadOnly(t *testing.T) {
	v := New(tempVaultPath(t))
	if err := v.Init("pass-123456789"); err != nil {
		t.Fatal(err)
	}
	if err := v.Add(agent.Agent{Name: "a", Provider: agent.ProviderClaude, Tags: []string{"t"}}); err != nil {
		t.Fatal(err)
	}
	c, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	c.List()[0].Tags[0] = "changed"
	if v.List()[0].Tags[0] != "t" {
		t.Fatal("snapshot shares memory with the vault")
	}
	if c.Unlocked() || c.Add(agent.Agent{Name: "b", Provider: agent.ProviderClaude}) == nil {
		t.Fatal("snapshot can save")
	}
	if c.Path() != v.Path() || len(c.List()) != 1 {
		t.Fatalf("snapshot path=%q agents=%v", c.Path(), c.List())
	}
}
