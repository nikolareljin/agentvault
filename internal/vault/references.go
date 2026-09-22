package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// ClosureResult is what a reference walk stored and what it would not.
type ClosureResult struct {
	Stored  []agent.InstructionFile
	Refused []agent.Reference
	Missing []agent.Reference
}

// PullReferenceClosure stores every file the vault's instructions name, and
// every file those name in turn, reading them from dir.
//
// An instruction file is not self-contained: a workspace AGENTS.md naming three
// workflow templates as required reading exports, without this, a rule that
// points at nothing on the next machine.
//
// The visited set is what terminates a cycle. Two documents naming each other
// is an ordinary thing for documentation to do, and the walk must not care.
func (v *Vault) PullReferenceClosure(dir string) (ClosureResult, error) {
	var result ClosureResult

	visited := map[string]bool{}
	var queue []agent.InstructionFile
	for _, inst := range v.ListInstructions() {
		visited[filepath.ToSlash(inst.Filename)] = true
		queue = append(queue, inst)
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		base := filepath.Dir(filepath.FromSlash(cur.Filename))
		for _, ref := range agent.ResolveReferences(dir, base, cur.Content) {
			switch ref.Status {
			case agent.ReferenceRefused:
				result.Refused = append(result.Refused, ref)
				continue
			case agent.ReferenceNotFound:
				result.Missing = append(result.Missing, ref)
				continue
			case agent.ReferenceSkipped:
				continue
			}
			if visited[ref.Rel] {
				continue
			}
			visited[ref.Rel] = true

			data, err := os.ReadFile(filepath.Join(dir, ref.Rel))
			if err != nil {
				// It was classified as a present regular file a moment ago, so
				// this is a race or a permission problem, not prose naming a
				// file that is not there.
				return result, fmt.Errorf("reading referenced %s: %w", ref.Rel, err)
			}
			inst := agent.InstructionFile{
				Name:      ref.Rel,
				Filename:  ref.Rel,
				Content:   string(data),
				UpdatedAt: time.Now(),
			}
			if err := v.SetInstruction(inst); err != nil {
				return result, err
			}
			result.Stored = append(result.Stored, inst)
			queue = append(queue, inst)
		}
	}

	result.Refused = dedupeReferences(result.Refused)
	result.Missing = dedupeReferences(result.Missing)
	return result, nil
}

func dedupeReferences(in []agent.Reference) []agent.Reference {
	seen := map[string]bool{}
	var out []agent.Reference
	for _, r := range in {
		if seen[r.Raw] {
			continue
		}
		seen[r.Raw] = true
		out = append(out, r)
	}
	return out
}
