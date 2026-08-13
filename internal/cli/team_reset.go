package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

type teamResetPlan struct {
	Scope string   `json:"scope"`
	Roots []string `json:"roots"`
	Cache []string `json:"cache,omitempty"`
}

func runTeamReset(args []string) error {
	flags := flag.NewFlagSet("team reset", flag.ContinueOnError)
	all := flags.Bool("all", false, "reset AMQ and amq-squad operational data across local repositories")
	dryRun := flags.Bool("dry-run", false, "print exact targets without changing them")
	yes := flags.Bool("yes", false, "confirm destructive reset")
	jsonOut := flags.Bool("json", false, "emit JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if !*dryRun && !*yes {
		return usageErrorf("team reset requires --yes (or use --dry-run)")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	plan := teamResetPlan{Scope: "project"}
	if *all {
		plan.Scope = "all"
		plan.Roots, err = discoverAllAMQResetRoots()
		plan.Cache = globalAMQResetCaches()
	} else {
		plan.Roots, err = discoverAMQResetRoots(cwd)
		plan.Roots = directProjectResetRoots(cwd, plan.Roots)
	}
	if err != nil {
		return err
	}
	plan.Roots = cleanResetTargets(plan.Roots)
	plan.Cache = cleanResetTargets(plan.Cache)
	if *dryRun {
		return printTeamResetPlan(plan, *jsonOut)
	}

	if err := stopRecordedResetAgents(plan.Roots); err != nil {
		return err
	}
	if err := runAMQReset(plan.Scope == "all", cwd); err != nil {
		return err
	}
	for _, root := range plan.Roots {
		switch filepath.Base(root) {
		case ".amq-squad", ".agent-mail":
			if err := removeAMQResetRoot(root); err != nil {
				return err
			}
		case "agent-mail":
			if isAMQMailboxRoot(root) {
				if err := os.RemoveAll(root); err != nil {
					return fmt.Errorf("remove legacy AMQ root %s: %w", root, err)
				}
			}
		}
	}
	if *jsonOut {
		return printTeamResetPlan(plan, true)
	}
	fmt.Printf("Reset complete: %d operational root(s), %d cache target(s).\n", len(plan.Roots), len(plan.Cache))
	return nil
}

func directProjectResetRoots(project string, discovered []string) []string {
	for _, name := range []string{".agent-mail", ".amq-squad"} {
		path := filepath.Join(project, name)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			discovered = append(discovered, path)
		}
	}
	return discovered
}

func discoverAllAMQResetRoots() ([]string, error) {
	roots := []string{}
	catalogRoots, err := discoverCatalogAMQResetRoots()
	if err != nil {
		return nil, err
	}
	for _, root := range catalogRoots {
		roots = append(roots, root)
		if squad := findSquadRootForAMQRoot(root); squad != "" {
			roots = append(roots, squad)
		}
	}
	for _, base := range resetSearchBases() {
		found, err := discoverAMQResetRoots(base)
		if err != nil {
			return nil, err
		}
		roots = append(roots, found...)
	}
	return cleanResetTargets(roots), nil
}

func discoverCatalogAMQResetRoots() ([]string, error) {
	cmd := exec.Command("amq", "reset", "--dry-run", "--json")
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspect AMQ reset catalog: %w", err)
	}
	var report struct {
		Roots []struct {
			Root string `json:"root"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("decode AMQ reset catalog: %w", err)
	}
	roots := make([]string, 0, len(report.Roots))
	for _, entry := range report.Roots {
		roots = append(roots, entry.Root)
	}
	return cleanResetTargets(roots), nil
}

func findSquadRootForAMQRoot(root string) string {
	current := filepath.Dir(filepath.Clean(root))
	for range 4 {
		candidate := filepath.Join(current, ".amq-squad")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ""
}

func resetSearchBases() []string {
	bases := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, home)
	}
	if drive := strings.TrimSpace(os.Getenv("SystemDrive")); drive != "" {
		dev := filepath.Join(drive+string(os.PathSeparator), "dev")
		if info, err := os.Stat(dev); err == nil && info.IsDir() {
			bases = append(bases, dev)
		}
	}
	if info, err := os.Stat(`D:\dev`); err == nil && info.IsDir() {
		bases = append(bases, `D:\dev`)
	}
	return cleanResetTargets(bases)
}

func discoverAMQResetRoots(base string) ([]string, error) {
	base, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	roots := []string{}
	err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if name == ".amq-squad" || name == ".agent-mail" || (name == "agent-mail" && isAMQMailboxRoot(path)) {
			roots = append(roots, path)
			return filepath.SkipDir
		}
		if path != base && shouldSkipResetSearchDir(name) {
			return filepath.SkipDir
		}
		return nil
	})
	return cleanResetTargets(roots), err
}

func shouldSkipResetSearchDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "vendor", "target", "bin", "obj", "appdata", "windows", "$recycle.bin", "system volume information":
		return true
	}
	return false
}

func isAMQMailboxRoot(path string) bool {
	info, err := os.Stat(filepath.Join(path, "meta", "config.json"))
	return err == nil && !info.IsDir()
}

func cleanResetTargets(paths []string) []string {
	seen := map[string]string{}
	for _, path := range paths {
		if path = strings.TrimSpace(path); path == "" {
			continue
		}
		clean, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		seen[strings.ToLower(filepath.Clean(clean))] = filepath.Clean(clean)
	}
	out := make([]string, 0, len(seen))
	for _, path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func globalAMQResetCaches() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "amq"),
		filepath.Join(os.Getenv("APPDATA"), "amq", "roots.json"),
		filepath.Join(home, ".claude", "agent-names", "leases"),
		filepath.Join(home, ".claude", "agent-names", "sessions"),
	}
}

func stopRecordedResetAgents(roots []string) error {
	seen := map[int]bool{}
	for _, root := range roots {
		if !isAMQMailboxRoot(root) && filepath.Base(root) != ".agent-mail" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || entry.Name() != "launch.json" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			var record launch.Record
			if json.Unmarshal(raw, &record) != nil || record.AgentPID <= 0 || seen[record.AgentPID] {
				return nil
			}
			if defaultDuplicateLaunchProbe.PIDAlive == nil || !defaultDuplicateLaunchProbe.PIDAlive(record.AgentPID) {
				return nil
			}
			if defaultDuplicateLaunchProbe.ProcessMatch == nil || !defaultDuplicateLaunchProbe.ProcessMatch(record.AgentPID, launchRecordProcessMatcher(record.Binary, record.Launcher)) {
				return fmt.Errorf("reset refused: live pid %d from %s does not match its recorded agent identity", record.AgentPID, path)
			}
			if !record.StartedAt.IsZero() && defaultDuplicateLaunchProbe.ProcessStartTime != nil {
				if started, ok := defaultDuplicateLaunchProbe.ProcessStartTime(record.AgentPID); ok && started.After(record.StartedAt.Add(launchProcessStartSkewEpsilon)) {
					return fmt.Errorf("reset refused: pid %d from %s was reused", record.AgentPID, path)
				}
			}
			seen[record.AgentPID] = true
			process, err := os.FindProcess(record.AgentPID)
			if err == nil {
				_ = process.Kill()
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func runAMQReset(all bool, project string) error {
	args := []string{"reset", "--yes"}
	if !all {
		root := filepath.Join(project, ".agent-mail")
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return nil
		}
		args = append(args, "--root", root)
	}
	cmd := exec.Command("amq", args...)
	cmd.Dir = project
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("amq reset: %w", err)
	}
	return nil
}

func removeAMQResetRoot(path string) error {
	path = filepath.Clean(path)
	base := filepath.Base(path)
	if base != ".agent-mail" && base != ".amq-squad" {
		return fmt.Errorf("refuse unsafe reset target %s", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func printTeamResetPlan(plan teamResetPlan, jsonOut bool) error {
	if jsonOut {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(plan)
	}
	fmt.Printf("AMQ team reset (%s):\n", plan.Scope)
	for _, path := range plan.Roots {
		fmt.Printf("  root: %s\n", path)
	}
	for _, path := range plan.Cache {
		fmt.Printf("  cache: %s\n", path)
	}
	return nil
}
