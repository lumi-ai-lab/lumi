//go:build !windows

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/requestercontext"
)

const runAsPiSpawnHelperEnv = "LUMI_RUN_AS_PI_SPAWN_HELPER"

func TestBuiltInPiRealRunAsSpawnsFromPathWithCredentialHome(t *testing.T) {
	if os.Getenv(runAsPiSpawnHelperEnv) == "1" {
		runAuthenticatedFakePi()
		os.Exit(0)
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires Linux root to exercise real UID/GID spawn boundaries")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}

	parent, err := os.MkdirTemp("", "lumi-run-as-pi-spawn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o711); err != nil {
		t.Fatal(err)
	}
	uid, gid, readerGID := uint32(62501), uint32(62502), uint32(62503)
	home := filepath.Join(parent, "pi-home")
	authPath := filepath.Join(home, ".pi", "agent", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte("credential-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := chownRunAsTree(home, uid, gid); err != nil {
		t.Fatal(err)
	}

	helper := stageRunAsPiHelper(t, parent)
	binDir := filepath.Join(parent, "npm", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	piPath := filepath.Join(binDir, "pi")
	wrapper := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestBuiltInPiRealRunAsSpawnsFromPathWithCredentialHome$ -- \"$@\"\n", helper)
	if err := os.WriteFile(piPath, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	observationPath := filepath.Join(home, ".pi", "agent", "sessions", "spawn-observation.json")
	if err := os.MkdirAll(filepath.Dir(observationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(filepath.Dir(observationPath), int(uid), int(gid)); err != nil {
		t.Fatal(err)
	}

	requesterRoot := filepath.Join(parent, "requester-context")
	t.Setenv(requestercontext.EnvRequesterContextRoot, requesterRoot)
	t.Setenv(requestercontext.EnvRequesterContextReaderGID, strconv.FormatUint(uint64(readerGID), 10))
	cfg := &config.AgentConfig{
		ID: "pi", Name: "PI", Command: "npx", Args: []string{"-y", config.PiACPPackageSpec},
		RunAsUID: &uid, RunAsGID: &gid, SupplementaryGIDs: []uint32{readerGID},
		Env: map[string]string{
			"HOME":                       home,
			"PI_CODING_AGENT_DIR":        filepath.Join(home, ".pi", "agent"),
			"PATH":                       binDir + ":/usr/local/bin:/usr/bin:/bin",
			runAsPiSpawnHelperEnv:        "1",
			"LUMI_RUN_AS_PI_OBSERVATION": observationPath,
		},
	}
	bridge := startRunAsTestBridge(t, cfg)
	if err := bridge.request(1, "initialize", map[string]any{
		"protocolVersion": 1, "clientCapabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "run-as-test", "version": "1"},
	}); err != nil {
		t.Fatalf("initialize run-as built-in Pi bridge: %v", err)
	}
	if err := bridge.request(2, "session/new", map[string]any{"cwd": workspace, "mcpServers": []any{}}); err != nil {
		t.Fatalf("authenticated session/new through run-as Pi: %v", err)
	}

	data, err := os.ReadFile(observationPath)
	if err != nil {
		t.Fatal(err)
	}
	var observation struct {
		UID    int      `json:"uid"`
		GID    int      `json:"gid"`
		Groups []int    `json:"groups"`
		Argv   []string `json:"argv"`
	}
	if err := json.Unmarshal(data, &observation); err != nil {
		t.Fatal(err)
	}
	if observation.UID != int(uid) || observation.GID != int(gid) || !containsInt(observation.Groups, int(readerGID)) {
		t.Fatalf("run-as Pi identity = uid %d gid %d groups %v", observation.UID, observation.GID, observation.Groups)
	}
	if len(observation.Argv) < 2 || observation.Argv[0] != "--mode" || observation.Argv[1] != "rpc" {
		t.Fatalf("Pi argv = %q", observation.Argv)
	}
}

type runAsTestBridge struct {
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	cmd    *exec.Cmd
}

func startRunAsTestBridge(t *testing.T, cfg *config.AgentConfig) *runAsTestBridge {
	t.Helper()
	command, args, err := processCommand(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	for key, value := range cfg.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}
	if err := configureCommand(cmd, cfg); err != nil {
		cancel()
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	bridge := &runAsTestBridge{stdin: stdin, stdout: bufio.NewScanner(stdout), cmd: cmd}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
	})
	return bridge
}

func (bridge *runAsTestBridge) request(id int, method string, params any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := bridge.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write bridge request: %w", err)
	}
	for bridge.stdout.Scan() {
		var response struct {
			ID    int             `json:"id"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(bridge.stdout.Bytes(), &response) != nil || response.ID != id {
			continue
		}
		if len(response.Error) > 0 && string(response.Error) != "null" {
			return fmt.Errorf("bridge RPC error")
		}
		return nil
	}
	return fmt.Errorf("bridge stopped before response")
}

func stageRunAsPiHelper(t *testing.T, parent string) string {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(parent, "run-as-pi-helper")
	destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	return target
}

func chownRunAsTree(root string, uid, gid uint32) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return os.Chmod(path, 0o600)
	})
}

func runAuthenticatedFakePi() {
	args := argsAfterRunAsDoubleDash(os.Args)
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("0.83.0")
		return
	}
	home := os.Getenv("HOME")
	auth, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "auth.json"))
	if err != nil || string(auth) != "credential-marker" {
		fmt.Fprintln(os.Stderr, "credential unavailable")
		os.Exit(1)
	}
	groups, _ := os.Getgroups()
	observation := map[string]any{"uid": os.Geteuid(), "gid": os.Getegid(), "groups": groups, "argv": args}
	encoded, _ := json.Marshal(observation)
	if err := os.WriteFile(os.Getenv("LUMI_RUN_AS_PI_OBSERVATION"), encoded, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "session runtime unavailable")
		os.Exit(1)
	}
	sessionFile := filepath.Join(home, ".pi", "agent", "sessions", fmt.Sprintf("session-%d.jsonl", os.Getpid()))
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(1)
		}
		response := map[string]any{"type": "response", "id": request["id"], "success": true}
		switch request["type"] {
		case "get_state":
			response["data"] = map[string]any{"sessionFile": sessionFile, "model": map[string]string{"provider": "fake", "id": "model"}, "thinkingLevel": "off"}
		case "get_available_models":
			response["data"] = map[string]any{"models": []map[string]string{{"provider": "fake", "id": "model", "name": "Fake"}}}
		}
		data, _ := json.Marshal(response)
		writer.Write(append(data, '\n'))
		writer.Flush()
	}
}

func argsAfterRunAsDoubleDash(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
