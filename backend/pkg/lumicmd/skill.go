package lumicmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/pengmide/lumi/internal/skillstore"
)

func runSkill(args []string, stdout *os.File, programName string) error {
	if len(args) == 0 {
		printSkillUsage(stdout, programName)
		return nil
	}
	switch args[0] {
	case "add":
		return runSkillAdd(args[1:], stdout)
	case "list", "ls":
		return runSkillList(args[1:], stdout)
	case "rm", "remove", "delete", "del":
		return runSkillDelete(args[1:], stdout)
	case "enable":
		return runSkillToggle(args[1:], stdout, true)
	case "disable":
		return runSkillToggle(args[1:], stdout, false)
	case "sync":
		return runSkillSync(args[1:], stdout)
	case "-h", "--help", "help":
		printSkillUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown skill command: %s", args[0])
	}
}

func runSkillAdd(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("skill add", flag.ContinueOnError)
	fs.SetOutput(stdout)
	name := fs.String("name", "", "Skill name (required)")
	display := fs.String("display-name", "", "Optional display name")
	desc := fs.String("description", "", "Optional description")
	srcType := fs.String("source-type", "local", "Source: local | git | archive")
	srcPath := fs.String("path", "", "Local source path (--source-type local)")
	srcURL := fs.String("url", "", "Git source URL (--source-type git)")
	srcRef := fs.String("ref", "", "Git source ref/branch")
	srcSubdir := fs.String("subdir", "", "Subdirectory inside the source")
	srcArchive := fs.String("archive-key", "", "Archive key in ~/.lumi/skills/_archives/")
	apps := fs.String("apps", "all", "Comma-separated apps: claude,codex,qwen or 'all'")
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("skill add requires --name")
	}
	source := map[string]any{"type": strings.TrimSpace(*srcType)}
	switch strings.TrimSpace(*srcType) {
	case "local":
		if strings.TrimSpace(*srcPath) == "" {
			return errors.New("local skill requires --path")
		}
		source["path"] = strings.TrimSpace(*srcPath)
	case "git":
		if strings.TrimSpace(*srcURL) == "" {
			return errors.New("git skill requires --url")
		}
		source["url"] = strings.TrimSpace(*srcURL)
		if r := strings.TrimSpace(*srcRef); r != "" {
			source["ref"] = r
		}
	case "archive":
		if strings.TrimSpace(*srcArchive) == "" {
			return errors.New("archive skill requires --archive-key")
		}
		source["uploadKey"] = strings.TrimSpace(*srcArchive)
	default:
		return fmt.Errorf("unknown source-type: %s", *srcType)
	}
	if sub := strings.TrimSpace(*srcSubdir); sub != "" {
		source["subdir"] = sub
	}
	_, skillApps := storeAppsFromCSV(*apps)
	payload := map[string]any{
		"name":        strings.TrimSpace(*name),
		"displayName": strings.TrimSpace(*display),
		"description": strings.TrimSpace(*desc),
		"source":      source,
		"apps":        skillApps,
	}
	var result struct {
		Skill skillstore.Record `json:"skill"`
	}
	if err := apiRequestWithBase(*apiBase, http.MethodPost, "/skills/store", nil, payload, &result); err != nil {
		return fmt.Errorf("skill add: %w", err)
	}
	fmt.Fprintf(stdout, "Created %s %s (apps=%s)\n", result.Skill.ID, result.Skill.Name, skillAppsToCSV(result.Skill.Apps))
	return nil
}

func runSkillList(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("skill list", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	var result struct {
		Skills []skillstore.Record `json:"skills"`
	}
	if err := apiRequestWithBase(*apiBase, http.MethodGet, "/skills/store", nil, nil, &result); err != nil {
		return fmt.Errorf("skill list: %w", err)
	}
	sort.SliceStable(result.Skills, func(i, j int) bool { return result.Skills[i].Name < result.Skills[j].Name })
	for _, sk := range result.Skills {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", sk.ID, sk.Name, string(sk.Source.Type), skillAppsToCSV(sk.Apps))
	}
	return nil
}

func runSkillDelete(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("skill rm", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("skill rm requires <id>")
	}
	if err := apiRequestWithBase(*apiBase, http.MethodDelete, "/skills/store/"+fs.Arg(0), nil, nil, nil); err != nil {
		return fmt.Errorf("skill rm: %w", err)
	}
	fmt.Fprintf(stdout, "Deleted %s\n", fs.Arg(0))
	return nil
}

func runSkillToggle(args []string, stdout *os.File, enable bool) error {
	fs := flag.NewFlagSet("skill toggle", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apps := fs.String("apps", "", "Comma-separated apps to toggle")
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("requires <id>")
	}
	id := fs.Arg(0)
	current, err := getSkillRecord(*apiBase, id)
	if err != nil {
		return err
	}
	_, skillApps := storeAppsFromCSV(*apps)
	if skillApps.Claude {
		current.Apps.Claude = enable
	}
	if skillApps.Codex {
		current.Apps.Codex = enable
	}
	if skillApps.Qwen {
		current.Apps.Qwen = enable
	}
	patch, _ := json.Marshal(map[string]any{"apps": current.Apps})
	if err := apiRequestWithBase(*apiBase, http.MethodPatch, "/skills/store/"+id, nil, json.RawMessage(patch), nil); err != nil {
		return fmt.Errorf("skill toggle: %w", err)
	}
	fmt.Fprintf(stdout, "Updated %s apps=%s\n", id, skillAppsToCSV(current.Apps))
	return nil
}

func runSkillSync(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("skill sync", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := apiRequestWithBase(*apiBase, http.MethodPost, "/skills/store/sync", nil, nil, nil); err != nil {
		return fmt.Errorf("skill sync: %w", err)
	}
	fmt.Fprintln(stdout, "Skill sync triggered")
	return nil
}

func getSkillRecord(apiBase, id string) (skillstore.Record, error) {
	var result struct {
		Skills []skillstore.Record `json:"skills"`
	}
	if err := apiRequestWithBase(apiBase, http.MethodGet, "/skills/store", nil, nil, &result); err != nil {
		return skillstore.Record{}, err
	}
	for _, s := range result.Skills {
		if s.ID == id {
			return s, nil
		}
	}
	return skillstore.Record{}, fmt.Errorf("skill %s not found", id)
}

func printSkillUsage(stdout *os.File, programName string) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintf(stdout, "  %s skill add --name <name> --source-type local --path <dir> [--apps claude,codex,qwen]\n", programName)
	fmt.Fprintf(stdout, "  %s skill add --name <name> --source-type git --url <url> [--ref main] [--subdir <path>]\n", programName)
	fmt.Fprintf(stdout, "  %s skill add --name <name> --source-type archive --archive-key <key>\n", programName)
	fmt.Fprintf(stdout, "  %s skill list\n", programName)
	fmt.Fprintf(stdout, "  %s skill enable <id> --apps codex\n", programName)
	fmt.Fprintf(stdout, "  %s skill disable <id> --apps codex\n", programName)
	fmt.Fprintf(stdout, "  %s skill rm <id>\n", programName)
	fmt.Fprintf(stdout, "  %s skill sync\n", programName)
}
