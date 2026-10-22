package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// fromAnyProjectUsage is the help text of --from-any-project.
const fromAnyProjectUsage = "also write referenced files pulled from another project, or of unknown project"

// instructionProjectKey identifies the project dir belongs to, for the
// files its instructions refer to. A git checkout is its remote, else its
// root commits (locations.ProjectKey), so another clone of the same
// repository is the same project; a directory below the top level adds its
// path from there, because references resolve against dir. Anything else
// is its resolved absolute path.
func instructionProjectKey(dir string, identify locations.Identifier) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if rp, err := filepath.EvalSymlinks(abs); err == nil {
		abs = rp
	}
	if identify == nil {
		identify = locations.DefaultIdentifier
	}
	if r, err := identify(context.Background(), abs); err == nil && (r.Remote != "" || len(r.RootCommits) > 0) {
		key := locations.ProjectKey(locations.Project{GitRemote: r.Remote, RootCommits: r.RootCommits})
		if rel, err := filepath.Rel(r.Root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			key += "#" + foldPathCase(filepath.ToSlash(rel))
		}
		return key
	}
	return "path:" + foldPathCase(filepath.ToSlash(filepath.Clean(abs)))
}

// foldPathCase lower-cases p where the file system ignores case, so one
// directory gives one key however it was typed.
func foldPathCase(p string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}

// scopeReferencedFiles drops the referenced files that do not belong to
// project, unless anyProject, and returns them separately. Instructions
// themselves are shared across projects and always kept. With anyProject,
// one copy per filename is kept: project's, else the first.
func scopeReferencedFiles(insts []agent.InstructionFile, project string, anyProject bool) (keep, skipped []agent.InstructionFile) {
	isReferenced := agent.ReferencedFileClassifier(insts)
	chosen := map[string]int{}
	if anyProject {
		for i, inst := range insts {
			if !isReferenced(inst) {
				continue
			}
			f := filepath.ToSlash(inst.Filename)
			if j, ok := chosen[f]; !ok || (inst.SourceProject == project && insts[j].SourceProject != project) {
				chosen[f] = i
			}
		}
	}
	for i, inst := range insts {
		switch {
		case !isReferenced(inst):
			keep = append(keep, inst)
		case anyProject && chosen[filepath.ToSlash(inst.Filename)] == i:
			keep = append(keep, inst)
		case !anyProject && inst.SourceProject == project:
			keep = append(keep, inst)
		default:
			skipped = append(skipped, inst)
		}
	}
	// Another project's copy of a file this project's copy writes is not
	// missing here, so it is not listed.
	written := map[string]bool{}
	for _, inst := range keep {
		written[filepath.ToSlash(inst.Filename)] = true
	}
	listed := skipped[:0]
	for _, inst := range skipped {
		if !written[filepath.ToSlash(inst.Filename)] {
			listed = append(listed, inst)
		}
	}
	return keep, listed
}

// reportSkippedReferences names each skipped file and its source project,
// one line each, and how to write them anyway.
func reportSkippedReferences(w io.Writer, skipped []agent.InstructionFile) {
	if len(skipped) == 0 {
		return
	}
	unknown := false
	for _, inst := range skipped {
		src := inst.SourceProject
		if src == "" {
			src = "unknown"
			unknown = true
		}
		fmt.Fprintf(w, "  Skipped %s: referenced file from project %s\n", printable(inst.Filename), printable(src))
	}
	if unknown {
		fmt.Fprintln(w, "  A referenced file of unknown project was stored before agentvault recorded projects;")
		fmt.Fprintln(w, "  pull its project again to record it.")
	}
	fmt.Fprintf(w, "  %d referenced file(s) from another project not written; --from-any-project writes them.\n", len(skipped))
}

// remapInstructionProjects rewrites, for this machine, the path of each
// path-keyed source project that came from the bundle, as import rewrites
// every other path. A git key names no path and is kept, and so is a key
// this vault had that the bundle did not bring. A rewritten copy that lands
// on a copy this vault already has for that project is one entry: the
// bundle's when bundleWins, else this vault's.
func remapInstructionProjects(insts, fromBundle []agent.InstructionFile, remaps []pathRemap, bundleWins bool) ([]agent.InstructionFile, bool) {
	if len(remaps) == 0 {
		return insts, false
	}
	// A key made on macOS or Windows is lower-cased (foldPathCase) while the
	// bundle's paths keep their case, so each rewrite is tried folded too.
	folded := append([]pathRemap(nil), remaps...)
	for _, r := range remaps {
		if low := strings.ToLower(r.From); low != r.From {
			folded = append(folded, pathRemap{From: low, To: r.To})
		}
	}
	remaps = folded
	bundleKeys := map[string]bool{}
	for _, inst := range fromBundle {
		bundleKeys[inst.SourceProject] = true
	}
	changed := false
	out := make([]agent.InstructionFile, len(insts))
	for i, inst := range insts {
		if p, ok := strings.CutPrefix(inst.SourceProject, "path:"); ok && bundleKeys[inst.SourceProject] {
			if np, n := remapText(p, remaps); n > 0 {
				key := "path:" + foldPathCase(filepath.ToSlash(filepath.Clean(filepath.FromSlash(np))))
				if info, err := os.Stat(np); err == nil && info.IsDir() {
					// Keyed the way push will key it here: links resolved,
					// and a git identity if the checkout has one.
					key = instructionProjectKey(np, nil)
				}
				changed = changed || key != inst.SourceProject
				inst.SourceProject = key
			}
		}
		out[i] = inst
	}
	if !changed {
		return out, false
	}
	return dedupeRemapped(out, insts, bundleWins), true
}

// dedupeRemapped keeps one entry per key of out. before is out ahead of
// the rewrite: an entry whose key it changed came from the bundle.
func dedupeRemapped(out, before []agent.InstructionFile, bundleWins bool) []agent.InstructionFile {
	at := map[string]int{}
	var kept []agent.InstructionFile
	for i, inst := range out {
		key := agent.InstructionKey(inst)
		j, dup := at[key]
		if !dup {
			at[key] = len(kept)
			kept = append(kept, inst)
			continue
		}
		if rewritten := inst.SourceProject != before[i].SourceProject; rewritten == bundleWins {
			kept[j] = inst
		}
	}
	return kept
}

// baseKey is inst's key without its source project: the slot each
// project's copy of one referenced file shares.
func baseKey(inst agent.InstructionFile) string {
	inst.SourceProject = ""
	return agent.InstructionKey(inst)
}

// pickProjectCopy chooses among entries sharing one base key: the copy of
// project, else the one with no project, else the only one. Two or more
// other projects' copies and none of project's is ambiguous.
func pickProjectCopy(entries []agent.InstructionFile, project string) (agent.InstructionFile, bool, error) {
	var none, other []agent.InstructionFile
	for _, e := range entries {
		switch e.SourceProject {
		case project:
			return e, true, nil
		case "":
			none = append(none, e)
		default:
			other = append(other, e)
		}
	}
	if len(none) > 0 {
		return none[0], true, nil
	}
	switch len(other) {
	case 0:
		return agent.InstructionFile{}, false, nil
	case 1:
		return other[0], true, nil
	}
	return agent.InstructionFile{}, false, fmt.Errorf("%s is stored for %d projects; run this from the project's directory", printable(entries[0].Name), len(other))
}

// withSourceProject sets the source project of inst, stored by hand. A
// referenced file stays one: with project (pull) it is that project's copy,
// and a copy stored before projects were recorded is returned in stale to
// be removed; with project "" (set) it replaces the copy of the current
// directory's project, else the only copy. A file nothing names that
// replaces no referenced file stays an instruction.
func withSourceProject(v *vault.Vault, inst agent.InstructionFile, project string) (out agent.InstructionFile, stale string, err error) {
	key := baseKey(inst)
	var same []agent.InstructionFile
	all := []agent.InstructionFile{inst}
	for _, e := range v.ListInstructions() {
		if baseKey(e) == key {
			same = append(same, e)
			continue
		}
		all = append(all, e)
	}
	hasProject := false
	for _, e := range same {
		hasProject = hasProject || e.SourceProject != ""
	}
	referenced := hasProject || agent.ReferencedFileClassifier(append(all, same...))(inst)
	if !referenced {
		return inst, "", nil
	}
	if project != "" {
		inst.SourceProject = project
		for _, e := range same {
			if e.SourceProject == "" {
				stale = agent.InstructionKey(e)
			}
		}
		return inst, stale, nil
	}
	prev, ok, err := pickProjectCopy(same, instructionProjectKey(".", nil))
	if err != nil {
		return inst, "", err
	}
	if ok {
		inst.SourceProject = prev.SourceProject
	}
	return inst, "", nil
}

// findByName returns the stored entry named name, in scope and pattern when
// scope is set, choosing among projects' copies by the current directory.
func findByName(v *vault.Vault, name, scope, pattern string) (agent.InstructionFile, bool, error) {
	if scope == "" {
		inst, ok := v.GetInstruction(name)
		if !ok {
			return inst, false, nil
		}
		scope, pattern = inst.Scope, inst.DirectoryPattern
	}
	key := baseKey(agent.InstructionFile{Name: name, Scope: scope, DirectoryPattern: pattern})
	var same []agent.InstructionFile
	for _, e := range v.ListInstructions() {
		if baseKey(e) == key {
			same = append(same, e)
		}
	}
	return pickProjectCopy(same, instructionProjectKey(".", nil))
}
