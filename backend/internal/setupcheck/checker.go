package setupcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pengmide/lumi/internal/acppatch"
	"github.com/pengmide/lumi/internal/config"
)

// DependencyItem represents a single dependency check item.
type DependencyItem struct {
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	Package string `json:"package,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Install string `json:"install,omitempty"`
}

// SetupStatus represents the overall setup readiness.
type SetupStatus struct {
	Ready       bool             `json:"ready"`
	Environment []DependencyItem `json:"environment"`
	Agents      []DependencyItem `json:"agents"`
	ACPPackages []DependencyItem `json:"acpPackages"`
}

var installInstructions = map[string]string{
	"npm":    "https://nodejs.org/en/download",
	"npx":    "https://nodejs.org/en/download",
	"node":   "https://nodejs.org/en/download",
	"claude": "npm install -g @anthropic-ai/claude-code",
	"codex":  "npm install -g @openai/codex",
	"qwen":   "npm install -g @qwen-code/qwen-code",
	"pi":     "npm install -g @earendil-works/pi-coding-agent",
}

var acpToAgentCommand = map[string]struct {
	Name    string
	Command string
}{
	"@agentclientprotocol/claude-agent-acp": {Name: "Claude", Command: "claude"},
	"@zed-industries/claude-agent-acp":      {Name: "Claude", Command: "claude"},
	"@zed-industries/claude-code-acp":       {Name: "Claude", Command: "claude"},
	"@zed-industries/codex-acp":             {Name: "Codex", Command: "codex"},
	"@qwen-code/qwen-code":                  {Name: "Qwen Code", Command: "qwen"},
	"pi-acp":                                {Name: "PI", Command: "pi"},
}

// InitialStatus builds the initial "checking" state for the provided agents.
func InitialStatus(agents []config.AgentConfig) SetupStatus {
	status := SetupStatus{
		Ready: false,
		Environment: []DependencyItem{
			{Name: "node", Command: "node", Status: "checking", Message: "Checking..."},
			{Name: "npm", Command: "npm", Status: "checking", Message: "Checking..."},
			{Name: "npx", Command: "npx", Status: "checking", Message: "Checking..."},
		},
	}

	requiredAgents := map[string]struct {
		Name    string
		Command string
	}{}

	for _, agentCfg := range agents {
		if agentCfg.Command == "npx" {
			pkgSpec := extractPackageName(agentCfg.Command, agentCfg.Args)
			if pkgSpec != "" {
				status.ACPPackages = append(status.ACPPackages, DependencyItem{
					Name:    agentCfg.Name,
					Package: pkgSpec,
					Status:  "checking",
					Message: "Waiting...",
				})
				if agentInfo, ok := acpToAgentCommand[normalizePackageName(pkgSpec)]; ok {
					requiredAgents[agentInfo.Command] = agentInfo
				}
				continue
			}
		}

		if agentCfg.Command != "" {
			requiredAgents[agentCfg.Command] = struct {
				Name    string
				Command string
			}{
				Name:    agentCfg.Name,
				Command: agentCfg.Command,
			}
		}
	}

	agentKeys := make([]string, 0, len(requiredAgents))
	for key := range requiredAgents {
		agentKeys = append(agentKeys, key)
	}
	sort.Strings(agentKeys)

	for _, key := range agentKeys {
		agentInfo := requiredAgents[key]
		status.Agents = append(status.Agents, DependencyItem{
			Name:    agentInfo.Name,
			Command: agentInfo.Command,
			Status:  "checking",
			Message: "Checking...",
		})
	}

	return status
}

// Check evaluates the setup status for the provided agents.
func Check(agents []config.AgentConfig) SetupStatus {
	status := InitialStatus(agents)

	npmReady := commandExists("npm")
	npxReady := commandExists("npx")

	piConfigured := requiresPi(agents)
	for i := range status.Environment {
		item := &status.Environment[i]
		if !commandExists(item.Command) {
			item.Status = "missing"
			item.Message = "Not found"
			item.Install = installInstructions[item.Command]
			continue
		}
		if item.Command == "node" && piConfigured && !nodeSatisfies("22.19.0") {
			item.Status = "outdated"
			item.Message = "Requires Node.js >=22.19.0 for PI"
			item.Install = installInstructions[item.Command]
			continue
		}
		item.Status = "ready"
		item.Message = "Installed"
	}

	allAgentsReady := true
	for i := range status.Agents {
		item := &status.Agents[i]
		if commandExists(item.Command) {
			item.Status = "ready"
			item.Message = "Installed"
			continue
		}

		item.Status = "missing"
		item.Message = "Not found"
		item.Install = installInstructions[item.Command]
		allAgentsReady = false
	}

	allACPReady := true
	for i := range status.ACPPackages {
		item := &status.ACPPackages[i]
		switch {
		case !npmReady || !npxReady:
			item.Status = "blocked"
			item.Message = "Requires npm/npx"
			allACPReady = false
		case acppatch.IsTargetPiACP(item.Package):
			patchStatus := acppatch.Status(acppatch.RuntimeOptions{})
			if patchStatus.Applied {
				item.Status = "ready"
				item.Message = patchStatus.Message
				break
			}
			item.Status = "not_installed"
			item.Message = patchStatus.Message
			item.Install = "npm install -g --prefix " + acppatch.RuntimePrefix() + " " + item.Package
			allACPReady = false
		case isPackageCached(item.Package):
			item.Status = "ready"
			item.Message = "Cached"
		default:
			item.Status = "not_installed"
			item.Message = "Not installed"
			item.Install = "npm install -g " + item.Package
			allACPReady = false
		}
	}

	status.Ready = environmentReady(status.Environment) && allAgentsReady && allACPReady
	return status
}

func extractPackageName(command string, args []string) string {
	if command != "npx" || len(args) == 0 {
		return ""
	}

	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}

	return ""
}

func normalizePackageName(packageSpec string) string {
	if packageSpec == "" {
		return ""
	}

	if strings.HasPrefix(packageSpec, "@") {
		slashIndex := strings.Index(packageSpec, "/")
		if slashIndex == -1 {
			return packageSpec
		}
		versionIndex := strings.Index(packageSpec[slashIndex+1:], "@")
		if versionIndex == -1 {
			return packageSpec
		}
		return packageSpec[:slashIndex+1+versionIndex]
	}

	versionIndex := strings.Index(packageSpec, "@")
	if versionIndex == -1 {
		return packageSpec
	}
	return packageSpec[:versionIndex]
}

func commandExists(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

func requiresPi(agents []config.AgentConfig) bool {
	for _, agentCfg := range agents {
		if normalizePackageName(extractPackageName(agentCfg.Command, agentCfg.Args)) == "pi-acp" {
			return true
		}
		if strings.ToLower(strings.TrimSpace(agentCfg.ID)) == "pi" {
			return true
		}
	}
	return false
}

func nodeSatisfies(minVersion string) bool {
	cmd := exec.Command("node", "--version")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return compareSemver(strings.TrimSpace(string(output)), minVersion) >= 0
}

func compareSemver(actual, minimum string) int {
	actual = strings.TrimPrefix(strings.TrimSpace(actual), "v")
	minimum = strings.TrimPrefix(strings.TrimSpace(minimum), "v")
	actualParts := strings.Split(actual, ".")
	minParts := strings.Split(minimum, ".")
	for i := 0; i < 3; i++ {
		a := semverPart(actualParts, i)
		m := semverPart(minParts, i)
		if a > m {
			return 1
		}
		if a < m {
			return -1
		}
	}
	return 0
}

func semverPart(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	value := 0
	for _, r := range parts[index] {
		if r < '0' || r > '9' {
			break
		}
		value = value*10 + int(r-'0')
	}
	return value
}

func isPackageCached(packageName string) bool {
	packageName = normalizePackageName(packageName)
	if packageName == "" {
		return false
	}

	cmd := exec.Command("npm", "list", "-g", "--depth=0", packageName)
	if err := cmd.Run(); err == nil {
		return true
	}

	home, _ := os.UserHomeDir()
	cacheDirs := []string{filepath.Join(home, ".npm", "_npx")}
	if isWindows() {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			cacheDirs = append(cacheDirs, filepath.Join(localAppData, "npm-cache", "_npx"))
		}
		if appData := os.Getenv("APPDATA"); appData != "" {
			cacheDirs = append(cacheDirs, filepath.Join(appData, "npm-cache", "_npx"))
		}
	}

	for _, cacheDir := range cacheDirs {
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pkgPath := filepath.Join(cacheDir, entry.Name(), "node_modules", packageName, "package.json")
			if _, err := os.Stat(pkgPath); err == nil {
				return true
			}
		}
	}

	return false
}

func isWindows() bool {
	return os.PathSeparator == '\\' && os.PathListSeparator == ';'
}
