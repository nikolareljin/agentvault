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
// themselves are shared across projects and always kept.
func scopeReferencedFiles(insts []agent.InstructionFile, project string, anyProject bool) (keep, skipped []agent.InstructionFile) {
	for _, inst := range insts {
		if anyProject || !agent.IsReferencedFile(inst) || inst.SourceProject == project {
			keep = append(keep, inst)
			continue
		}
		skipped = append(skipped, inst)
	}
	return keep, skipped
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
// this vault had that the bundle did not bring.
func remapInstructionProjects(insts, fromBundle []agent.InstructionFile, remaps []pathRemap) ([]agent.InstructionFile, bool) {
	if len(remaps) == 0 {
		return insts, false
	}
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
	return out, changed
}
