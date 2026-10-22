package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/fsname"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var importCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import settings from an exported bundle",
	Long: `Import agents, shared config, rules, roles, instructions, provider configs,
workflow templates and assets from a bundle produced by 'agentvault export'.

Portable bundles (schema 2.x), older setup bundles (schema 1.x) and legacy
vault exports are all accepted; the format is detected automatically.

With no file argument the newest bundle in the default export directory is used:

  ` + "`<config dir>/exports`" + `

Conflict handling is chosen with --strategy:

  merge     keep the existing vault value, add only what is missing (default)
  replace   the bundle wins on every collision, local-only items are kept
  mirror    the bundle wins and local-only items are deleted, so this machine
            ends up matching the bundle exactly. Mirrors one profile at a time,
            since mirroring several into one vault would leave only the last.

A local file that differs from the bundle's is backed up and replaced, or
kept: import asks on a terminal, and --on-conflict backup-replace|keep decides
without asking. Commands the bundle adds or changes (hooks, statusLine, MCP
servers, the router's langgraph_command, .aider.conf.yml, scripts that
instructions refer to) are left out unless --keep-bundle-commands is given.

MCP servers are merged one by one: servers only this machine has stay. On a
terminal, import lists the bundle's new servers to pick from and asks about
each changed one; --mcp-servers a,b|all|none decides without asking. A picked
server's command is taken. Without a terminal or the flag, nothing new is
added and changed servers keep this machine's definition.

Use --strategy mirror to make several machines converge on one configuration.
Mirror deletes data, so it asks for confirmation unless --confirm is given.

A bundle can carry several profiles. Use --list to see them, --profile NAME to
import one, or --profile all to import every profile into this vault.

The bundle password is read from AGENTVAULT_IMPORT_PASSWORD when set.

Examples:
  agentvault import                                   # newest bundle, merge
  agentvault import team.avbundle --strategy mirror
  agentvault import team.avbundle --list
  agentvault import team.avbundle --profile work --dry-run
  agentvault import team.avbundle --strategy replace --apply-provider-configs
  agentvault import team.avbundle --apply-projects --project-root ~/src/app
  agentvault import team.avbundle --map /mnt/work=/Volumes/work --dry-run

Paths from the exporting machine are rewritten for this one: its home directory
becomes this home directory, its --project directory becomes --project-root, and
each --map OLD=NEW is applied first. --dry-run prints every rewrite.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runImport,
}

func init() {
	rootCmd.AddCommand(importCmd)
	importCmd.Flags().String("strategy", string(strategyMerge), "conflict handling: merge, replace, or mirror")
	importCmd.Flags().String("profile", "", "profile to import from the bundle (name, or 'all')")
	importCmd.Flags().Bool("list", false, "list the profiles in the bundle and exit")
	importCmd.Flags().Bool("dry-run", false, "report what would change without writing to the vault")
	importCmd.Flags().Bool("apply-provider-configs", false, "apply provider configs and provider asset files to the system after import")
	importCmd.Flags().Bool("confirm", false, "skip the confirmation prompt for destructive strategies")
	importCmd.Flags().StringArray("map", nil, "rewrite a path prefix from the exporting machine, OLD=NEW (repeatable); the home directory is rewritten without it")
	importCmd.Flags().Bool("apply-projects", false, "write the bundle's project files (instructions, rules, agents, commands) into --project-root, or into the confirmed match of the bundle's project")
	importCmd.Flags().String("project-root", "", "directory to write project files into with --apply-projects (default: the confirmed match of the bundle's project)")
	importCmd.Flags().StringArray("workdir-map", nil, "place a work dir from the bundle here, OLD=NEW (repeatable); projects under it match by identity")
	importCmd.Flags().Bool("no-match", false, "do not look for the bundle's projects on this machine; rewrite paths only by --map and the home directory")
	importCmd.Flags().Bool("accept-matches", false, "apply computed project matches without asking (for non-interactive imports)")
	importCmd.Flags().Bool("keep-bundle-commands", false, "keep hook, statusLine, MCP server, router and script commands that the bundle adds or changes; left out by default")
	importCmd.Flags().String("mcp-servers", "", "MCP servers to take from the bundle, new or changed: a comma-separated list of names, all, or none (default: ask on a terminal, else none)")
	importCmd.Flags().String("on-conflict", "", "when a local file differs from the bundle: backup-replace or keep (default: ask on a terminal, else backup-replace; --strategy merge means keep)")
	importCmd.Flags().Bool("keep-interpreter-allows", false, "keep Claude permissions such as Bash(bash:*) or Bash(python3:*) that let any program run without asking; left out by default")
	importCmd.Flags().Bool("plain", false, "deprecated: the export format is detected automatically")
	_ = importCmd.Flags().MarkDeprecated("plain", "the export format is detected automatically")
}

func runImport(cmd *cobra.Command, args []string) error {
	strategy, err := parseImportStrategy(mustString(cmd, "strategy"))
	if err != nil {
		return err
	}
	listOnly, _ := cmd.Flags().GetBool("list")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	applyConfigs, _ := cmd.Flags().GetBool("apply-provider-configs")
	confirmFlag, _ := cmd.Flags().GetBool("confirm")
	profileName := mustString(cmd, "profile")
	assetOpts, err := importAssetOptionsFromFlags(cmd, applyConfigs)
	if err != nil {
		return err
	}
	assetOpts.Overwrite = strategy != strategyMerge
	assetOpts.Apply.OnConflict = resolveConflictPolicy(assetOpts.Apply.OnConflict, cmd.Flags().Changed("strategy"), strategy)

	inPath := ""
	if len(args) == 1 {
		inPath = args[0]
	} else {
		if inPath, err = newestBundleInDefaultDir(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Using newest bundle: %s\n", inPath)
	}

	raw, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("reading import file: %w", err)
	}
	payload, err := decodeImportPayload(raw)
	if err != nil {
		return err
	}
	bundle, format, err := decodePortableBundle(payload)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if listOnly {
		printBundleProfiles(cmd, bundle, format)
		return nil
	}

	selected, err := selectProfilesForImport(cmd, bundle, profileName)
	if err != nil {
		return err
	}

	// Mirroring several profiles into one vault is incoherent: each profile would
	// delete what the previous one just added, leaving only the last. Profiles are
	// separate configurations, so ask which one this machine should match.
	if strategy.deletesLocalOnly() && len(selected) > 1 {
		return fmt.Errorf("--strategy mirror makes this vault match one profile exactly, but %d profiles are selected (%s); pick one with --profile NAME, or use --strategy replace to combine them",
			len(selected), strings.Join(profileNamesOf(selected), ", "))
	}

	interactive := term.IsTerminal(stdinFD())
	stdin := bufio.NewReader(os.Stdin)
	if strategy.deletesLocalOnly() && !dryRun {
		if err := confirmDestructiveImport(confirmFlag, interactive, stdin, cmd.ErrOrStderr(), selected); err != nil {
			return err
		}
	}
	assetOpts.Apply.Interactive = interactive
	assetOpts.Apply.In = stdin
	assetOpts.Apply.Out = cmd.ErrOrStderr()
	assetOpts.Apply.BundleTime = bundle.CreatedAt

	locOpts, err := locationOptionsFromFlags(cmd, interactive, dryRun, stdin, assetOpts.Maps)
	if err != nil {
		return err
	}

	v, err := openVault()
	if err != nil {
		return err
	}
	locOpts.Known = v.Locations()

	fmt.Fprintf(out, "Importing %s from %s (created %s, strategy %s)\n",
		format, bundleOrigin(bundle), bundle.CreatedAt.Format("2006-01-02 15:04"), strategy)
	if dryRun {
		fmt.Fprintln(out, "Dry run: no changes are written.")
	}

	var allMissed []missedEntry
	now := time.Now()
	total := importReport{}
	for _, profile := range selected {
		fmt.Fprintf(out, "\nProfile %q:\n", profile.Name)
		plan := planProfileImport(v, profile.Setup, strategy, now, importConsent{KeepCommands: assetOpts.Apply.KeepBundleCommands, MCP: assetOpts.Apply.MCPServers})
		for _, line := range plan.Report.Lines {
			fmt.Fprintln(out, line)
		}
		if len(plan.Report.Lines) == 0 {
			fmt.Fprintln(out, "  No changes.")
		}
		total.Added += plan.Report.Added
		total.Updated += plan.Report.Updated
		total.Skipped += plan.Report.Skipped
		total.Removed += plan.Report.Removed

		profileOpts := assetOpts
		match, err := resolveBundleLocations(cmd.Context(), profile.Setup.Locations, profile.Setup.SourceProject, locOpts)
		if err != nil {
			return err
		}
		profileOpts.Computed = match.Remaps
		profileOpts.NoProjectWrites = match.Refused
		if profileOpts.ProjectRoot == "" {
			profileOpts.ProjectRoot = match.ProjectRoot
		}
		allMissed = append(allMissed, match.Missed...)
		if profileOpts.ApplyProjects && profileOpts.ProjectRoot == "" && match.SourceSkipped {
			// Listed in the summary at the end; the rest of the import goes on.
			profileOpts.ApplyProjects = false
		}
		if profileOpts.ApplyProjects && profileOpts.ProjectRoot == "" {
			if match.Refused {
				return errors.New("--apply-projects: the bundle's project was matched but not confirmed; pass --accept-matches, or name the directory with --project-root")
			}
			return errors.New("--apply-projects needs --project-root: the directory to write project files into (no confirmed match for the bundle's project)")
		}

		if dryRun {
			previewProfileAssets(cmd, profile.Setup, profileOpts)
			continue
		}
		if err := commitProfileImport(v, plan); err != nil {
			return err
		}
		if err := importProfileAssets(cmd, profile.Setup, profileOpts); err != nil {
			return err
		}
		if err := recordLocations(v, match, &locOpts); err != nil {
			return err
		}
	}

	for _, n := range mcpUnknownNames(assetOpts) {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: --mcp-servers: no MCP server %s in the bundle\n", strconv.Quote(n))
	}
	printBroadAllowWarning(cmd.ErrOrStderr(), assetOpts.Apply.Broad)
	if len(assetOpts.Apply.Backups) > 0 {
		fmt.Fprintln(out, "\nBackups of replaced files:")
		for _, b := range assetOpts.Apply.Backups {
			fmt.Fprintf(out, "  %s\n", printable(b))
		}
	}
	fmt.Fprintf(out, "\nSummary: %d added, %d updated, %d skipped, %d removed\n",
		total.Added, total.Updated, total.Skipped, total.Removed)
	allMissed = dedupeMissed(allMissed)
	if len(allMissed) > 0 {
		printMissed(out, allMissed)
	}
	if dryRun {
		fmt.Fprintln(out, "Dry run complete; re-run without --dry-run to apply.")
		return nil
	}
	printInstallGuide(cmd, selected)
	if len(allMissed) > 0 {
		// Everything else was imported; the exit status says it was not all.
		return fmt.Errorf("%d work dir(s) or project(s) not imported; see \"Not imported\" above", len(allMissed))
	}
	return nil
}

// mustString reads a string flag, returning "" when the flag is absent.
func mustString(cmd *cobra.Command, name string) string {
	value, err := cmd.Flags().GetString(name)
	if err != nil {
		return ""
	}
	return value
}

// decodeImportPayload returns the plaintext JSON of an export file, decrypting
// it first when the file is not already JSON.
func decodeImportPayload(raw []byte) ([]byte, error) {
	if detectBundleFormat(raw) != bundleFormatUnknown {
		return raw, nil
	}
	if json.Valid(raw) {
		return nil, errors.New("file is valid JSON but not a recognized agentvault export")
	}
	password := os.Getenv(ImportPasswordEnv)
	if password == "" {
		if !term.IsTerminal(stdinFD()) {
			return nil, fmt.Errorf("bundle looks encrypted and stdin is not a terminal: set %s", ImportPasswordEnv)
		}
		var err error
		if password, err = readPassword("Bundle password: "); err != nil {
			return nil, err
		}
	}
	plaintext, err := decryptBundlePayload(raw, password)
	if err != nil {
		return nil, err
	}
	if detectBundleFormat(plaintext) == bundleFormatUnknown {
		return nil, errors.New("decrypted payload is not a recognized export format")
	}
	return plaintext, nil
}

// newestBundleInDefaultDir finds the most recently modified export in the
// default export directory.
func newestBundleInDefaultDir() (string, error) {
	dir := defaultExportDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no bundle given and none found in %s: %w", dir, err)
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var found []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != DefaultExportExtension && ext != ".json" && ext != ".bundle" && ext != ".vault" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, candidate{path: filepath.Join(dir, e.Name()), mod: info.ModTime()})
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no bundle given and no export files found in %s", dir)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	return found[0].path, nil
}

// selectProfilesForImport resolves which bundle profiles to apply. A bundle with
// one profile needs no choice; several profiles require --profile, or an
// interactive pick.
func selectProfilesForImport(cmd *cobra.Command, bundle PortableBundle, requested string) ([]PortableProfile, error) {
	if strings.EqualFold(strings.TrimSpace(requested), "all") {
		return bundle.Profiles, nil
	}
	if requested != "" {
		profile, ok := bundle.FindProfile(requested)
		if !ok {
			return nil, fmt.Errorf("profile %q not in bundle; available: %s", requested, strings.Join(bundle.ProfileNames(), ", "))
		}
		return []PortableProfile{profile}, nil
	}
	if len(bundle.Profiles) == 1 {
		return bundle.Profiles, nil
	}
	if !term.IsTerminal(stdinFD()) {
		return nil, fmt.Errorf("bundle holds %d profiles (%s); choose with --profile NAME or --profile all",
			len(bundle.Profiles), strings.Join(bundle.ProfileNames(), ", "))
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Bundle holds %d profiles:\n", len(bundle.Profiles))
	for i, p := range bundle.Profiles {
		fmt.Fprintf(out, "  %d) %-16s %d agents, %d rules, %d roles\n",
			i+1, p.Name, len(p.Setup.Agents), len(p.Setup.SharedConfig.Rules), len(p.Setup.SharedConfig.Roles))
	}
	reader := bufio.NewReader(os.Stdin)
	answer, err := askLine(out, reader, "Profile to import (name, number, or 'all')", "all")
	if err != nil {
		return nil, err
	}
	answer = strings.TrimSpace(answer)
	if strings.EqualFold(answer, "all") {
		return bundle.Profiles, nil
	}
	for i, p := range bundle.Profiles {
		if answer == fmt.Sprint(i+1) || strings.EqualFold(answer, p.Name) {
			return []PortableProfile{p}, nil
		}
	}
	return nil, fmt.Errorf("profile %q not in bundle; available: %s", answer, strings.Join(bundle.ProfileNames(), ", "))
}

// profileNamesOf lists the names of the given profiles, in order.
func profileNamesOf(profiles []PortableProfile) []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names
}

// confirmDestructiveImport gates mirror imports, which delete local-only items.
func confirmDestructiveImport(confirmFlag bool, isTerminal bool, in io.Reader, w io.Writer, profiles []PortableProfile) error {
	if confirmFlag {
		return nil
	}
	names := profileNamesOf(profiles)
	if !isTerminal {
		return fmt.Errorf("--strategy mirror deletes vault items that are not in the bundle; re-run with --confirm to proceed non-interactively")
	}
	fmt.Fprintf(w, "Strategy 'mirror' makes this vault match the bundle exactly.\n")
	fmt.Fprintf(w, "Anything not in profile %s will be DELETED: agents, rules, roles,\n", strings.Join(names, ", "))
	fmt.Fprintf(w, "instructions, MCP servers, sessions, provider configs, pricing rows and\n")
	fmt.Fprintf(w, "model capability entries. The shared system prompt and router config are\n")
	fmt.Fprintf(w, "cleared too when the bundle does not carry them.\n")
	fmt.Fprint(w, "Type 'mirror' to continue: ")
	reader, ok := in.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(in)
	}
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(line)) != "mirror" {
		return errors.New("import cancelled")
	}
	return nil
}

// importProfileAssets writes the non-vault parts of a profile: workflow
// templates, staged portable assets, and optionally the live provider configs.
// mcpUnknownNames are --mcp-servers names no bundle server had, once
// provider configs were looked at.
func mcpUnknownNames(o importAssetOptions) []string {
	if !o.ApplyConfigs || o.Apply == nil {
		return nil
	}
	return o.Apply.MCPServers.unknown()
}

// importAssetOptions selects what import does with a bundle's files.
type importAssetOptions struct {
	ApplyConfigs  bool
	ApplyProjects bool
	ProjectRoot   string
	Maps          []string
	// Computed are the accepted rewrites from matching the bundle's
	// projects; --map rules win over them.
	Computed []pathRemap
	// NoProjectWrites is set when computed matches were not accepted:
	// project files are then not written.
	NoProjectWrites bool
	// Overwrite existing project files that differ. Follows --strategy:
	// merge keeps what is there; replace and mirror overwrite.
	Overwrite bool
	// Apply settles conflicts and records backups for the whole run.
	Apply *importApplyContext
}

func importAssetOptionsFromFlags(cmd *cobra.Command, applyConfigs bool) (importAssetOptions, error) {
	opts := importAssetOptions{ApplyConfigs: applyConfigs, Apply: newImportApplyContext()}
	opts.Apply.KeepBundleCommands, _ = cmd.Flags().GetBool("keep-bundle-commands")
	if cmd.Flags().Changed("mcp-servers") {
		sel, err := parseMCPSelection(mustString(cmd, "mcp-servers"))
		if err != nil {
			return opts, err
		}
		opts.Apply.MCPServers = sel
	}
	if v := mustString(cmd, "on-conflict"); v != "" {
		policy, err := parseConflictPolicy(v)
		if err != nil {
			return opts, err
		}
		opts.Apply.OnConflict = policy
	}
	opts.ApplyProjects, _ = cmd.Flags().GetBool("apply-projects")
	opts.ProjectRoot = mustString(cmd, "project-root")
	opts.Maps, _ = cmd.Flags().GetStringArray("map")
	keepInterpreterAllows, _ = cmd.Flags().GetBool("keep-interpreter-allows")
	if opts.ProjectRoot != "" {
		root, err := filepath.Abs(expandUserPath(opts.ProjectRoot))
		if err != nil {
			return opts, fmt.Errorf("--project-root: %w", err)
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return opts, fmt.Errorf("--project-root %s is not a directory", root)
		}
		opts.ProjectRoot = root
	}
	if _, err := buildPathRemaps("", "", "", "", opts.Maps); err != nil {
		return opts, err
	}
	return opts, nil
}

// remapProfileAssets rewrites the exporting machine's paths in every file of
// one profile for this machine, and returns what it changed.
func remapProfileAssets(setup SetupBundle, opts importAssetOptions) (SetupBundle, []string, error) {
	targetHome := exportSourceHome()
	remaps, err := buildPathRemaps(setup.SourceHome, targetHome, setup.SourceProject, opts.ProjectRoot, opts.Maps)
	if err != nil {
		return setup, nil, err
	}
	if len(opts.Computed) > 0 {
		userMaps, _ := buildPathRemaps("", "", "", "", opts.Maps)
		remaps = mergeComputedRemaps(remaps, opts.Computed, userMaps)
	}
	var report, r []string
	setup.ProviderFiles, r = remapAssets(setup.ProviderFiles, remaps, targetHome)
	report = append(report, r...)
	setup.ProjectFiles, r = remapAssets(setup.ProjectFiles, remaps, targetHome)
	report = append(report, r...)
	setup.SkillAssets, r = remapAssets(setup.SkillAssets, remaps, targetHome)
	report = append(report, r...)
	setup.InstructionOverrides, _ = remapAssets(setup.InstructionOverrides, remaps, targetHome)
	configs, n, err := remapProviderConfigs(setup.ProviderConfigs, remaps)
	if err != nil {
		return setup, report, fmt.Errorf("rewriting paths in provider configs: %w", err)
	}
	if n > 0 {
		setup.ProviderConfigs = configs
		report = append(report, fmt.Sprintf("rewrote %d path(s) in provider configs", n))
	}
	return setup, report, nil
}

// previewProfileAssets is the --dry-run view of importProfileAssets: the
// path rewrites, and with --apply-projects or --apply-provider-configs each
// file it would write, replace, keep or leave out. It writes nothing.
func previewProfileAssets(cmd *cobra.Command, setup SetupBundle, opts importAssetOptions) {
	out := cmd.OutOrStdout()
	setup, report, err := remapProfileAssets(setup, opts)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", printable(err.Error()))
		return
	}
	for _, line := range report {
		fmt.Fprintf(out, "  path: %s\n", printable(line))
	}
	ctx := opts.applyContext()
	dry := *ctx
	dry.DryRun = true
	if opts.ApplyProjects {
		for _, line := range previewProjectAssets(setup.ProjectFiles, opts.ProjectRoot, &dry) {
			fmt.Fprintf(out, "  project: %s\n", printable(line))
		}
	}
	if !opts.ApplyConfigs {
		return
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not resolve home directory: %v\n", err)
		return
	}
	assets, dropped := providerAssetsToApply(setup, homeDir, ctx.KeepBundleCommands)
	for _, d := range dropped {
		fmt.Fprintf(out, "  would: Codex provider config: leave out %s\n", printable(d))
	}
	_, lines, err := applyProviderAssetsWith(homeDir, assets, &dry)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", printable(err.Error()))
	}
	for _, line := range lines {
		fmt.Fprintf(out, "  would: %s\n", printable(line))
	}
	for _, line := range dry.MCPReport {
		fmt.Fprintf(out, "  MCP servers: %s\n", printable(line))
	}
}

// previewProjectAssets says, per project file, what --apply-projects would
// do in root, without staging or writing anything.
func previewProjectAssets(files []SetupAsset, root string, ctx *importApplyContext) []string {
	var out []string
	for _, asset := range files {
		if asset.Redacted || asset.Missing || !asset.ContentPresent {
			continue
		}
		rel := asset.ProjectRelativePath
		if rel == "" {
			rel = asset.LogicalPath
		}
		if reason := projectAssetRefusal(rel); reason != "" {
			out = append(out, fmt.Sprintf("%s: not written: %s", rel, reason))
			continue
		}
		dest, err := safeJoinUnder(root, rel)
		if err == nil {
			err = fsname.CheckBeforeWrite(root, dest)
		}
		if err != nil {
			out = append(out, fmt.Sprintf("%s: not written: %v", rel, err))
			continue
		}
		existing, err := readExistingFile(dest)
		switch {
		case errors.Is(err, os.ErrNotExist):
			out = append(out, fmt.Sprintf("%s: would write %s", rel, dest))
		case err != nil:
			out = append(out, fmt.Sprintf("%s: not written: %v", rel, err))
		case bytes.Equal(existing, asset.Content):
		default:
			out = append(out, fmt.Sprintf("%s: %s", rel, ctx.preview(dest)))
		}
	}
	return out
}

// providerAssetsToApply are the files --apply-provider-configs writes: the
// Claude and Codex provider configs as files, then the bundle's provider
// files and skills. It also returns what it left out of the Codex config.
func providerAssetsToApply(setup SetupBundle, homeDir string, keepCommands bool) ([]SetupAsset, []string) {
	configAssets := claudeConfigAssets(setup.ProviderConfigs.Claude)
	codexAssets, codexDropped := codexConfigAssets(setup.ProviderConfigs.Codex, homeDir, keepCommands)
	configAssets = append(configAssets, codexAssets...)
	// The bundle's own copy of a file wins over the config's (often older)
	// one, which is then not applied at all: applied first, it would be
	// written, backed up and reported, then replaced. A copy exported
	// without its content cannot win, so where this machine has the file,
	// the config's version must not replace it either.
	carried := map[[2]string]bool{}
	withheld := map[[2]string]bool{}
	for _, f := range setup.ProviderFiles {
		key := [2]string{f.LogicalRoot, f.LogicalPath}
		if f.Redacted {
			withheld[key] = true
		} else {
			carried[key] = true
		}
	}
	kept := configAssets[:0]
	for _, a := range configAssets {
		key := [2]string{a.LogicalRoot, a.LogicalPath}
		if carried[key] || (withheld[key] && localFileExists(homeDir, a)) {
			continue
		}
		kept = append(kept, a)
	}
	return append(append(kept, setup.ProviderFiles...), providerSkillAssets(setup.SkillAssets)...), codexDropped
}

func importProfileAssets(cmd *cobra.Command, setup SetupBundle, opts importAssetOptions) error {
	applyConfigs := opts.ApplyConfigs
	setup, remapReport, err := remapProfileAssets(setup, opts)
	if err != nil {
		return err
	}
	for _, line := range remapReport {
		fmt.Fprintf(cmd.OutOrStdout(), "  path: %s\n", printable(line))
	}
	if setup.Templates.SchemaVersion != "" || len(setup.Templates.Assets) > 0 {
		imported, warnings, err := workflowtemplates.ImportBundle(resolveConfigDir(), setup.Templates)
		if err != nil {
			return fmt.Errorf("importing workflow templates: %w", err)
		}
		if imported > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  Imported: workflow templates (%d)\n", imported)
		}
		for _, warn := range warnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", printable(warn))
		}
	}

	// An instruction override (the project's AGENTS.md or CLAUDE.md) goes to
	// the vault and is left out of the stage, except with --apply-projects:
	// it is the file the user asked to have in the project.
	filteredProjectFiles := setup.ProjectFiles
	if !opts.ApplyProjects {
		filteredProjectFiles = filterProjectFilesForStaging(setup.ProjectFiles, setup.InstructionOverrides)
	}
	assets := append(append(append([]SetupAsset{}, setup.ProviderFiles...), filteredProjectFiles...), setup.SkillAssets...)
	staged, stageWarnings, err := stageImportedAssets(effectiveConfigDir(), assets)
	if err != nil {
		return fmt.Errorf("staging imported portable assets: %w", err)
	}
	if staged > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Imported: portable assets (%d staged)\n", staged)
	}
	for _, warn := range stageWarnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", printable(warn))
	}

	printPluginInstallCommands(cmd.OutOrStdout(), setup.Plugins)
	if opts.ApplyProjects {
		// The writer refuses symlinks on the way to a file; the root the user
		// named may sit under one (/tmp on macOS). Paths in files keep the
		// spelling the user gave.
		writeRoot := opts.ProjectRoot
		if resolved, err := filepath.EvalSymlinks(writeRoot); err == nil {
			writeRoot = resolved
		}
		applied, skipped, err := applyStagedProjectAssetsCtx(effectiveConfigDir(), writeRoot, opts.Overwrite, opts.applyContext())
		if err != nil {
			return fmt.Errorf("applying project files: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Applied: project files (%d) to %s\n", applied, opts.ProjectRoot)
		for _, s := range skipped {
			fmt.Fprintf(cmd.OutOrStdout(), "    not written: %q\n", s)
		}
	}
	if !applyConfigs {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "  Applying provider configs and assets to system...")
	ctx := opts.applyContext()
	// The Claude provider config becomes settings.json and keybindings.json,
	// applied like the bundle's own files: commands and interpreter allows
	// checked, secrets kept, local keys kept, the old file backed up.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not resolve home directory: %v\n", err)
		return nil
	}
	providerAssets, codexDropped := providerAssetsToApply(setup, homeDir, ctx.KeepBundleCommands)
	for _, d := range codexDropped {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: Codex provider config: left out %s\n", printable(d))
	}
	applied, warnings, err := applyProviderAssetsWith(homeDir, providerAssets, ctx)
	if err != nil {
		return fmt.Errorf("applying provider assets: %w", err)
	}
	if applied > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Applied: provider assets (%d)\n", applied)
	}
	for _, warn := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", printable(warn))
	}
	for _, line := range ctx.MCPReport {
		fmt.Fprintf(cmd.OutOrStdout(), "  MCP servers: %s\n", printable(line))
	}
	ctx.MCPReport = nil
	return nil
}

// printBundleProfiles renders the --list output.
func printBundleProfiles(cmd *cobra.Command, bundle PortableBundle, format bundleFormat) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Format: %s\n", format)
	fmt.Fprintf(out, "Source: %s (%s), created %s\n",
		bundleOrigin(bundle), printable(bundle.SourceOS), bundle.CreatedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "Profiles: %d\n", len(bundle.Profiles))
	for _, p := range bundle.Profiles {
		fmt.Fprintf(out, "  %-16s %s\n", printable(p.Name), printable(p.ConfigDir))
		fmt.Fprintf(out, "    Agents: %d, Sessions: %d, Rules: %d, Roles: %d, Instructions: %d\n",
			len(p.Setup.Agents), len(p.Setup.Sessions.Sessions), len(p.Setup.SharedConfig.Rules),
			len(p.Setup.SharedConfig.Roles), len(p.Setup.SharedConfig.Instructions))
		fmt.Fprintf(out, "    Templates: %d, Provider files: %d, Skill assets: %d\n",
			len(p.Setup.Templates.Assets), len(p.Setup.ProviderFiles), len(p.Setup.SkillAssets))
		printDeclinedAssets(out, p.Setup.Declined)
	}
}

// printDeclinedAssets lists what the export left behind, separating a refusal
// from an absence. Both end as a file that is not here; only one is a decision
// somebody can revisit, and a restore that is missing something should say
// which kind it is rather than leaving the reader to guess.
func printDeclinedAssets(out io.Writer, declined []DeclinedAsset) {
	if len(declined) == 0 {
		return
	}
	byCategory := map[string][]DeclinedAsset{}
	for _, d := range declined {
		byCategory[d.Category] = append(byCategory[d.Category], d)
	}
	repoNote := ""
	if n := len(byCategory[DeclinedRepoScope]); n > 0 {
		repoNote = fmt.Sprintf(", %d repository settings", n)
	}
	fmt.Fprintf(out, "    Declined: %d (%d refused by policy, %d not present%s)\n",
		len(declined), len(byCategory[DeclinedByPolicy]), len(byCategory[DeclinedAbsent]), repoNote)
	for _, d := range byCategory[DeclinedRepoScope] {
		fmt.Fprintf(out, "      %-8s %s: %s\n", DeclinedRepoScope, printable(d.Path), printable(d.Reason))
	}
	// Policy declines are few and each is a decision somebody can act on, so
	// they are all printed. Absent ones are mostly prose naming a file that was
	// never there -- eighteen of them against four policy declines on a real
	// instruction set -- and printing every one buries the ones that matter.
	// The bundle keeps the full list either way.
	const maxAbsentPrinted = 5
	for _, d := range byCategory[DeclinedByPolicy] {
		fmt.Fprintf(out, "      %-8s %s: %s\n", DeclinedByPolicy, printable(d.Path), printable(d.Reason))
	}
	absent := byCategory[DeclinedAbsent]
	for i, d := range absent {
		if i == maxAbsentPrinted {
			fmt.Fprintf(out, "      %-8s ... and %d more, all in the bundle\n",
				DeclinedAbsent, len(absent)-maxAbsentPrinted)
			break
		}
		fmt.Fprintf(out, "      %-8s %s: %s\n", DeclinedAbsent, printable(d.Path), printable(d.Reason))
	}
}

// bundleOrigin names the machine a bundle came from.
func bundleOrigin(bundle PortableBundle) string {
	if strings.TrimSpace(bundle.SourceMachine) != "" {
		return printable(bundle.SourceMachine)
	}
	return "unknown machine"
}

// printInstallGuide shows the requirements captured with the first profile that
// carries one, so a fresh machine knows what to install.
func printInstallGuide(cmd *cobra.Command, profiles []PortableProfile) {
	for _, p := range profiles {
		if len(p.Setup.InstallGuide.Requirements) == 0 {
			continue
		}
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "\n--- Installation Guide ---")
		fmt.Fprintln(out, "Requirements:")
		for _, req := range p.Setup.InstallGuide.Requirements {
			status := "optional"
			if req.Required {
				status = "required"
			}
			fmt.Fprintf(out, "  - %s (%s)\n", printable(req.Name), status)
			if req.InstallCmd != "" {
				fmt.Fprintf(out, "    Install: %s\n", printable(req.InstallCmd))
			}
		}
		return
	}
}

// applyContext is the run's context, or a fresh one when the options were
// built without flags.
func (o *importAssetOptions) applyContext() *importApplyContext {
	if o.Apply == nil {
		o.Apply = newImportApplyContext()
	}
	return o.Apply
}

// resolveConflictPolicy: --on-conflict wins; otherwise an explicit --strategy
// answers the question (merge keeps what is here, replace and mirror take the
// bundle's file); otherwise import asks, or backs up and replaces when it
// cannot ask.
func resolveConflictPolicy(flag conflictPolicy, strategyGiven bool, strategy importStrategy) conflictPolicy {
	if flag != conflictAsk || !strategyGiven {
		return flag
	}
	if strategy == strategyMerge {
		return conflictKeep
	}
	return conflictBackupReplace
}

// localFileExists reports whether the provider file a writes is on this
// machine. One whose place cannot be resolved counts as present.
func localFileExists(homeDir string, a SetupAsset) bool {
	dest, err := providerAssetDestination(homeDir, a)
	if err != nil {
		return true
	}
	_, err = os.Lstat(dest)
	return err == nil
}

// claudeConfigAssets are the files SaveClaudeConfig would write for c, as
// assets, so they go through the same checks as the bundle's own files.
func claudeConfigAssets(c *agent.ClaudeConfig) []SetupAsset {
	if c == nil {
		return nil
	}
	var out []SetupAsset
	// Exported without its settings: this machine's settings.json stays.
	if data, err := agent.ClaudeSettingsJSON(c); err == nil && !c.SettingsRedacted {
		out = append(out, SetupAsset{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json", ContentPresent: true, Content: data, RedactedFields: c.RedactedFields})
	}
	if len(c.Keybindings) > 0 {
		if data, err := json.MarshalIndent(c.Keybindings, "", "  "); err == nil {
			out = append(out, SetupAsset{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "keybindings.json", ContentPresent: true, Content: data})
		}
	}
	return out
}

// codexConfigAssets turns the Codex provider config into the files it
// would write, so they go through the asset checks (commands, conflicts,
// backups) instead of SaveCodexConfig, which rewrites config.toml in place
// and deletes local rules. A trusted project the bundle adds or changes is
// left out unless keepCommands: trust lets Codex run commands there. A rule
// whose name is not a plain file name is left out, flag or not. It returns
// the assets and what it left out; only a trusted project names the flag.
func codexConfigAssets(c *agent.CodexConfig, home string, keepCommands bool) ([]SetupAsset, []string) {
	if c == nil {
		return nil, nil
	}
	var dropped []string
	existing, _ := readExistingFile(filepath.Join(home, ".codex", "config.toml"))
	var local struct {
		Projects map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
	_, _ = toml.Decode(string(existing), &local)
	projects := map[string]string{}
	for p, v := range local.Projects {
		projects[p] = v.TrustLevel
	}
	for _, p := range sortedStringKeys(c.TrustedProjects) {
		level := c.TrustedProjects[p]
		if cur, ok := projects[p]; ok && cur == level {
			continue
		}
		if !keepCommands {
			dropped = append(dropped, fmt.Sprintf("trusted project %q (%s) (use --keep-bundle-commands to take it)", p, level))
			continue
		}
		projects[p] = level
	}
	out := []SetupAsset{{LogicalRoot: setupAssetRootProviderCodex, LogicalPath: "config.toml", ContentPresent: true,
		Content: []byte(agent.CodexConfigTOML(string(existing), projects))}}
	for _, name := range sortedStringKeys(c.Rules) {
		if !agent.ValidRuleName(name) {
			dropped = append(dropped, fmt.Sprintf("rule %q: not a plain file name", name))
			continue
		}
		out = append(out, SetupAsset{LogicalRoot: setupAssetRootProviderCodex, LogicalPath: "rules/" + name + ".md",
			ContentPresent: true, Content: []byte(c.Rules[name])})
	}
	return out, dropped
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// locationOptionsFromFlags reads the matching flags.
func locationOptionsFromFlags(cmd *cobra.Command, interactive, dryRun bool, in *bufio.Reader, maps []string) (locationOptions, error) {
	o := locationOptions{Interactive: interactive, DryRun: dryRun, In: in, Out: cmd.ErrOrStderr(), Home: exportSourceHome(), Asked: newAskMemo()}
	o.NoMatch, _ = cmd.Flags().GetBool("no-match")
	o.AcceptMatches, _ = cmd.Flags().GetBool("accept-matches")
	values, _ := cmd.Flags().GetStringArray("workdir-map")
	wm, err := parseWorkDirMaps(values)
	if err != nil {
		return o, err
	}
	o.WorkDirMaps = wm
	userMaps, err := buildPathRemaps("", "", "", "", maps)
	if err != nil {
		return o, err
	}
	o.UserMaps = userMaps
	return o, nil
}

// recordLocations stores one profile's accepted projects in the vault and
// refreshes what the next profile's alias checks see.
func recordLocations(v *vault.Vault, match locationResult, o *locationOptions) error {
	add := match.Stored
	for _, m := range match.NewMaps {
		if add.WorkDirMaps == nil {
			add.WorkDirMaps = map[string]string{}
		}
		add.WorkDirMaps[locations.CleanForeign(m.From)] = m.To
	}
	for _, m := range match.ConfirmedHidden {
		if add.ConfirmedHidden == nil {
			add.ConfirmedHidden = map[string]locations.HiddenConfirm{}
		}
		add.ConfirmedHidden[locations.CleanForeign(m.From)] = locations.HiddenConfirm{Target: m.To, Projects: m.Projects}
	}
	if add.Empty() {
		return nil
	}
	if err := v.SetLocations(locations.Merge(v.Locations(), add)); err != nil {
		return fmt.Errorf("recording project locations: %w", err)
	}
	o.Known = v.Locations()
	return nil
}
