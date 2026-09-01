package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
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
            ends up matching the bundle exactly

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
  agentvault import team.avbundle --strategy replace --apply-provider-configs`,
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

	interactive := term.IsTerminal(stdinFD())
	if strategy.deletesLocalOnly() && !dryRun {
		if err := confirmDestructiveImport(confirmFlag, interactive, os.Stdin, cmd.ErrOrStderr(), selected); err != nil {
			return err
		}
	}

	v, err := openVault()
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Importing %s from %s (created %s, strategy %s)\n",
		format, bundleOrigin(bundle), bundle.CreatedAt.Format("2006-01-02 15:04"), strategy)
	if dryRun {
		fmt.Fprintln(out, "Dry run: no changes are written.")
	}

	now := time.Now()
	total := importReport{}
	for _, profile := range selected {
		fmt.Fprintf(out, "\nProfile %q:\n", profile.Name)
		plan := planProfileImport(v, profile.Setup, strategy, now)
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

		if dryRun {
			continue
		}
		if err := commitProfileImport(v, plan); err != nil {
			return err
		}
		if err := importProfileAssets(cmd, profile.Setup, applyConfigs); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "\nSummary: %d added, %d updated, %d skipped, %d removed\n",
		total.Added, total.Updated, total.Skipped, total.Removed)
	if dryRun {
		fmt.Fprintln(out, "Dry run complete; re-run without --dry-run to apply.")
		return nil
	}
	printInstallGuide(cmd, selected)
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

// confirmDestructiveImport gates mirror imports, which delete local-only items.
func confirmDestructiveImport(confirmFlag bool, isTerminal bool, in io.Reader, w io.Writer, profiles []PortableProfile) error {
	if confirmFlag {
		return nil
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	if !isTerminal {
		return fmt.Errorf("--strategy mirror deletes vault items that are not in the bundle; re-run with --confirm to proceed non-interactively")
	}
	fmt.Fprintf(w, "Strategy 'mirror' makes this vault match the bundle exactly.\n")
	fmt.Fprintf(w, "Agents, rules, roles, instructions, MCP servers and sessions that are not in profile(s) %s will be DELETED.\n", strings.Join(names, ", "))
	fmt.Fprint(w, "Type 'mirror' to continue: ")
	reader := bufio.NewReader(in)
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
func importProfileAssets(cmd *cobra.Command, setup SetupBundle, applyConfigs bool) error {
	if setup.Templates.SchemaVersion != "" || len(setup.Templates.Assets) > 0 {
		imported, warnings, err := workflowtemplates.ImportBundle(resolveConfigDir(), setup.Templates)
		if err != nil {
			return fmt.Errorf("importing workflow templates: %w", err)
		}
		if imported > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  Imported: workflow templates (%d)\n", imported)
		}
		for _, warn := range warnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
		}
	}

	filteredProjectFiles := filterProjectFilesForStaging(setup.ProjectFiles, setup.InstructionOverrides)
	assets := append(append(append([]SetupAsset{}, setup.ProviderFiles...), filteredProjectFiles...), setup.SkillAssets...)
	staged, stageWarnings, err := stageImportedAssets(effectiveConfigDir(), assets)
	if err != nil {
		return fmt.Errorf("staging imported portable assets: %w", err)
	}
	if staged > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Imported: portable assets (%d staged)\n", staged)
	}
	for _, warn := range stageWarnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
	}

	if !applyConfigs {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "  Applying provider configs and assets to system...")
	if setup.ProviderConfigs.Claude != nil {
		if err := agent.SaveClaudeConfig(setup.ProviderConfigs.Claude); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not apply Claude config: %v\n", err)
		}
	}
	if setup.ProviderConfigs.Codex != nil {
		if err := agent.SaveCodexConfig(setup.ProviderConfigs.Codex); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not apply Codex config: %v\n", err)
		}
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not resolve home directory: %v\n", err)
		return nil
	}
	applied, warnings, err := applyProviderAssetsToSystem(homeDir, append(setup.ProviderFiles, providerSkillAssets(setup.SkillAssets)...))
	if err != nil {
		return fmt.Errorf("applying provider assets: %w", err)
	}
	if applied > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Applied: provider assets (%d)\n", applied)
	}
	for _, warn := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
	}
	return nil
}

// printBundleProfiles renders the --list output.
func printBundleProfiles(cmd *cobra.Command, bundle PortableBundle, format bundleFormat) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Format: %s\n", format)
	fmt.Fprintf(out, "Source: %s (%s), created %s\n",
		bundleOrigin(bundle), bundle.SourceOS, bundle.CreatedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "Profiles: %d\n", len(bundle.Profiles))
	for _, p := range bundle.Profiles {
		fmt.Fprintf(out, "  %-16s %s\n", p.Name, p.ConfigDir)
		fmt.Fprintf(out, "    Agents: %d, Sessions: %d, Rules: %d, Roles: %d, Instructions: %d\n",
			len(p.Setup.Agents), len(p.Setup.Sessions.Sessions), len(p.Setup.SharedConfig.Rules),
			len(p.Setup.SharedConfig.Roles), len(p.Setup.SharedConfig.Instructions))
		fmt.Fprintf(out, "    Templates: %d, Provider files: %d, Skill assets: %d\n",
			len(p.Setup.Templates.Assets), len(p.Setup.ProviderFiles), len(p.Setup.SkillAssets))
	}
}

// bundleOrigin names the machine a bundle came from.
func bundleOrigin(bundle PortableBundle) string {
	if strings.TrimSpace(bundle.SourceMachine) != "" {
		return bundle.SourceMachine
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
			fmt.Fprintf(out, "  - %s (%s)\n", req.Name, status)
			if req.InstallCmd != "" {
				fmt.Fprintf(out, "    Install: %s\n", req.InstallCmd)
			}
		}
		return
	}
}
