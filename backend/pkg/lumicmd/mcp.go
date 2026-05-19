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

	"github.com/pengmide/lumi/internal/mcpstore"
)

func runMCP(args []string, stdout *os.File, programName string) error {
	if len(args) == 0 {
		printMCPUsage(stdout, programName)
		return nil
	}
	switch args[0] {
	case "add":
		return runMCPAdd(args[1:], stdout)
	case "list", "ls":
		return runMCPList(args[1:], stdout)
	case "rm", "remove", "delete", "del":
		return runMCPDelete(args[1:], stdout)
	case "enable":
		return runMCPToggle(args[1:], stdout, true)
	case "disable":
		return runMCPToggle(args[1:], stdout, false)
	case "sync":
		return runMCPSync(args[1:], stdout)
	case "-h", "--help", "help":
		printMCPUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown mcp command: %s", args[0])
	}
}

func runMCPAdd(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	fs.SetOutput(stdout)
	name := fs.String("name", "", "MCP server name (required)")
	transport := fs.String("transport", "stdio", "Transport: stdio | http | sse")
	command := fs.String("command", "", "Process command for stdio transport")
	url := fs.String("url", "", "URL for http/sse transports")
	apps := fs.String("apps", "all", "Comma-separated apps: claude,codex,qwen or 'all'")
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	var argsFlag stringSliceFlag
	fs.Var(&argsFlag, "arg", "Append a positional arg (repeatable)")
	envFlag := storeKVFlag{}
	fs.Var(envFlag, "env", "Append KEY=VAL (repeatable, stdio only)")
	var headerFlag stringSliceFlag
	fs.Var(&headerFlag, "header", "Append HEADER:VALUE (repeatable, http/sse only)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("mcp add requires --name")
	}
	mcpApps, _ := storeAppsFromCSV(*apps)
	payload := map[string]any{
		"name":      strings.TrimSpace(*name),
		"transport": strings.TrimSpace(*transport),
		"apps":      mcpApps,
	}
	if t := strings.TrimSpace(*transport); t == "stdio" || t == "" {
		if strings.TrimSpace(*command) == "" {
			return errors.New("stdio MCP requires --command")
		}
		payload["command"] = strings.TrimSpace(*command)
	} else {
		if strings.TrimSpace(*url) == "" {
			return errors.New("http/sse MCP requires --url")
		}
		payload["url"] = strings.TrimSpace(*url)
	}
	if len(argsFlag) > 0 {
		payload["args"] = argsFlag.Slice()
	}
	if len(envFlag) > 0 {
		payload["env"] = map[string]string(envFlag)
	}
	if len(headerFlag) > 0 {
		payload["headers"] = parseHeaderFlags(headerFlag)
	}
	var result struct {
		Server mcpstore.Record `json:"server"`
	}
	if err := apiRequestWithBase(*apiBase, http.MethodPost, "/mcp/store", nil, payload, &result); err != nil {
		return fmt.Errorf("mcp add: %w", err)
	}
	fmt.Fprintf(stdout, "Created %s %s (apps=%s)\n", result.Server.ID, result.Server.Name, mcpAppsToCSV(result.Server.Apps))
	return nil
}

func runMCPList(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("mcp list", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	var result struct {
		Servers []mcpstore.Record `json:"servers"`
	}
	if err := apiRequestWithBase(*apiBase, http.MethodGet, "/mcp/store", nil, nil, &result); err != nil {
		return fmt.Errorf("mcp list: %w", err)
	}
	sort.SliceStable(result.Servers, func(i, j int) bool { return result.Servers[i].Name < result.Servers[j].Name })
	for _, srv := range result.Servers {
		summary := srv.Command
		if srv.Transport == mcpstore.TransportHTTP || srv.Transport == mcpstore.TransportSSE {
			summary = srv.URL
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", srv.ID, srv.Name, srv.Transport, mcpAppsToCSV(srv.Apps), summary)
	}
	return nil
}

func runMCPDelete(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("mcp rm", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("mcp rm requires <id>")
	}
	if err := apiRequestWithBase(*apiBase, http.MethodDelete, "/mcp/store/"+fs.Arg(0), nil, nil, nil); err != nil {
		return fmt.Errorf("mcp rm: %w", err)
	}
	fmt.Fprintf(stdout, "Deleted %s\n", fs.Arg(0))
	return nil
}

func runMCPToggle(args []string, stdout *os.File, enable bool) error {
	fs := flag.NewFlagSet("mcp toggle", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apps := fs.String("apps", "", "Comma-separated apps to toggle: claude,codex,qwen or 'all'")
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
	current, err := getMCPRecord(*apiBase, id)
	if err != nil {
		return err
	}
	apps2, _ := storeAppsFromCSV(*apps)
	if apps2.Claude {
		current.Apps.Claude = enable
	}
	if apps2.Codex {
		current.Apps.Codex = enable
	}
	if apps2.Qwen {
		current.Apps.Qwen = enable
	}
	patch, _ := json.Marshal(map[string]any{"apps": current.Apps})
	if err := apiRequestWithBase(*apiBase, http.MethodPatch, "/mcp/store/"+id, nil, json.RawMessage(patch), nil); err != nil {
		return fmt.Errorf("mcp toggle: %w", err)
	}
	fmt.Fprintf(stdout, "Updated %s apps=%s\n", id, mcpAppsToCSV(current.Apps))
	return nil
}

func runMCPSync(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("mcp sync", flag.ContinueOnError)
	fs.SetOutput(stdout)
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := apiRequestWithBase(*apiBase, http.MethodPost, "/mcp/store/sync", nil, nil, nil); err != nil {
		return fmt.Errorf("mcp sync: %w", err)
	}
	fmt.Fprintln(stdout, "MCP sync triggered")
	return nil
}

func getMCPRecord(apiBase, id string) (mcpstore.Record, error) {
	var result struct {
		Servers []mcpstore.Record `json:"servers"`
	}
	if err := apiRequestWithBase(apiBase, http.MethodGet, "/mcp/store", nil, nil, &result); err != nil {
		return mcpstore.Record{}, err
	}
	for _, s := range result.Servers {
		if s.ID == id {
			return s, nil
		}
	}
	return mcpstore.Record{}, fmt.Errorf("mcp %s not found", id)
}

func parseHeaderFlags(values []string) map[string]string {
	out := map[string]string{}
	for _, raw := range values {
		k, v, ok := strings.Cut(raw, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func printMCPUsage(stdout *os.File, programName string) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintf(stdout, "  %s mcp add --name <name> --transport stdio --command <cmd> [--arg <arg>]... [--env K=V]... [--apps claude,codex,qwen]\n", programName)
	fmt.Fprintf(stdout, "  %s mcp add --name <name> --transport http --url <url> [--header H:V]...\n", programName)
	fmt.Fprintf(stdout, "  %s mcp list\n", programName)
	fmt.Fprintf(stdout, "  %s mcp enable <id> --apps claude,codex\n", programName)
	fmt.Fprintf(stdout, "  %s mcp disable <id> --apps codex\n", programName)
	fmt.Fprintf(stdout, "  %s mcp rm <id>\n", programName)
	fmt.Fprintf(stdout, "  %s mcp sync\n", programName)
}
