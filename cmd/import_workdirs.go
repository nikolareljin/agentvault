package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/locations"
)

// filterImportWorkDirs applies import --workdirs to the selected profiles:
// everything a bundle work dir not named in spec owns is removed by the
// export filter, run on the exporting machine's paths, before anything is
// planned. The bundle has no per-name sections, so this is how a part of it
// is taken. An empty spec or "all" takes the whole bundle. It returns one
// line per work dir left out, with counts.
func filterImportWorkDirs(profiles []PortableProfile, spec string) ([]string, error) {
	want, all := workDirSpec(spec)
	if all {
		return nil, nil
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("--workdirs %q names no work dir; use all, or names from the bundle", spec)
	}
	// Names as the bundle writes them, invalid ones included: a work dir
	// whose name import would ignore is still not one the user asked for.
	count := map[string]int{}
	for _, p := range profiles {
		for _, n := range bundleWorkDirNames(p.Setup.Locations) {
			count[n]++
		}
	}
	if len(count) == 0 {
		return nil, fmt.Errorf("--workdirs %s: the bundle names no work dirs; import it without --workdirs", printable(spec))
	}
	var unknown []string
	for n := range want {
		if count[n] == 0 {
			unknown = append(unknown, printable(n))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("--workdirs: the bundle has no work dir named %s (it names: %s)", strings.Join(unknown, ", "), strings.Join(nameList(count), ", "))
	}
	var lines []string
	for i := range profiles {
		setup := &profiles[i].Setup
		if dup := duplicateNames(setup.Locations, want); len(dup) > 0 {
			return nil, fmt.Errorf("--workdirs: profile %q gives the name %s to more than one work dir; the bundle was edited, export it again", profiles[i].Name, strings.Join(dup, ", "))
		}
		if len(bundleWorkDirNames(setup.Locations)) == 0 {
			// Exported with no work dirs declared: nothing ties its
			// content to one, so all of it is taken.
			lines = append(lines, fmt.Sprintf("Profile %q: names no work dirs; --workdirs does not apply, all of it is imported", profiles[i].Name))
			continue
		}
		s := importWorkDirScope(setup, want)
		if len(s.unselected) == 0 {
			continue
		}
		if _, err := s.apply(setup, setup.SourceHome); err != nil {
			return nil, fmt.Errorf("profile %q: %w", profiles[i].Name, err)
		}
		for _, n := range s.unselected {
			if kinds := s.left[n.Name]; len(kinds) > 0 {
				lines = append(lines, fmt.Sprintf("Profile %q: work dir %s not selected with --workdirs; left out %s", profiles[i].Name, printable(n.Name), leftCounts(kinds)))
			}
		}
	}
	return lines, nil
}

// checkWorkDirMapsSelected refuses --workdir-map NAME=PATH for a work dir
// --workdirs leaves out: it names something this import does not take.
func checkWorkDirMapsSelected(spec string, names map[string]string) error {
	want, all := workDirSpec(spec)
	if all {
		return nil
	}
	var off []string
	for n := range names {
		if !want[n] {
			off = append(off, printable(n))
		}
	}
	if len(off) == 0 {
		return nil
	}
	sort.Strings(off)
	return fmt.Errorf("--workdir-map %s: --workdirs %s leaves that work dir out", strings.Join(off, ", "), printable(spec))
}

// checkWorkDirsStrategy refuses --strategy mirror with part of a bundle:
// mirror deletes what the bundle lacks, which would be this machine's copy
// of everything the other work dirs own.
func checkWorkDirsStrategy(spec string, strategy importStrategy) error {
	if _, all := workDirSpec(spec); all || !strategy.deletesLocalOnly() {
		return nil
	}
	return fmt.Errorf("--strategy mirror deletes what the bundle lacks, and --workdirs %s leaves the other work dirs out of it; use --strategy merge or replace", printable(spec))
}

// workDirSpec reads --workdirs: all is true for "" or "all", else want
// holds the names given.
func workDirSpec(spec string) (want map[string]bool, all bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == locations.AllWorkDirs {
		return nil, true
	}
	want = map[string]bool{}
	for _, name := range strings.Split(spec, ",") {
		if name = strings.TrimSpace(name); name != "" {
			want[name] = true
		}
	}
	return want, false
}

// importWorkDirScope is the export scope for one profile: its named work
// dirs split by want, with the bundle's paths.
func importWorkDirScope(setup *SetupBundle, want map[string]bool) workDirScope {
	osName, _, _ := strings.Cut(setup.SourceOS, "/")
	if osName == "" {
		osName = "unknown"
	}
	s := workDirScope{left: map[string]map[string]int{}, bundleOS: osName}
	if setup.Locations == nil {
		return s
	}
	// Instructions pulled from a project name it by identity; the bundle's
	// own project list ties that identity to a path. A copy, because the
	// filter rewrites the bundle's list.
	s.known.Projects = append([]locations.Project(nil), setup.Locations.Projects...)
	for _, w := range setup.Locations.WorkDirs {
		if w.Name == "" || strings.TrimSpace(w.Abs) == "" {
			continue
		}
		n := locations.Named{Name: w.Name, Path: w.Abs}
		if want[w.Name] {
			s.selected = append(s.selected, n)
		} else {
			s.unselected = append(s.unselected, n)
		}
	}
	return s
}

func bundleWorkDirNames(set *locations.Set) []string {
	if set == nil {
		return nil
	}
	var out []string
	for _, w := range set.WorkDirs {
		if w.Name != "" {
			out = append(out, w.Name)
		}
	}
	return out
}

// duplicateNames are the wanted names the set gives to two work dirs.
func duplicateNames(set *locations.Set, want map[string]bool) []string {
	count := map[string]int{}
	for _, n := range bundleWorkDirNames(set) {
		count[n]++
	}
	var dup []string
	for n, c := range count {
		if c > 1 && want[n] {
			dup = append(dup, printable(n))
		}
	}
	sort.Strings(dup)
	return dup
}

func leftCounts(kinds map[string]int) string {
	var parts []string
	for k, c := range kinds {
		parts = append(parts, fmt.Sprintf("%s: %d", k, c))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func nameList(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, printable(k))
	}
	sort.Strings(out)
	return out
}
