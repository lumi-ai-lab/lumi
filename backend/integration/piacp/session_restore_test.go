//go:build integration

package piacp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/config"
)

const fakePIHelperEnv = "LUMI_PI_ACP_FAKE_HELPER"

func TestPiACPLogicalSessionsRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the pinned pi-acp package")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx is required")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	stateDir := filepath.Join(root, "fake-pi-sessions")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{home, stateDir, binDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}

	helper := writeFakePIWrapper(t, binDir)
	env := append(os.Environ(),
		"HOME="+home,
		"PI_CODING_AGENT_DIR="+filepath.Join(home, ".pi", "agent"),
		"PI_ACP_PI_COMMAND="+helper,
		fakePIHelperEnv+"=1",
		"LUMI_PI_ACP_FAKE_STATE_DIR="+stateDir,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	adapter := startAdapter(t, env)
	sessionA := adapter.newSession(t, root)
	if got := adapter.prompt(t, sessionA, "A1"); got != "A1" {
		t.Fatalf("A1 history = %q, want A1", got)
	}

	sessionB := adapter.newSession(t, root)
	if got := adapter.prompt(t, sessionB, "B1"); got != "B1" {
		t.Fatalf("B1 history = %q, want B1", got)
	}
	if sessionA == sessionB {
		t.Fatalf("session IDs are equal: %s", sessionA)
	}

	if got := adapter.prompt(t, sessionA, "A2"); got != "A1|A2" {
		t.Fatalf("restored A history = %q, want A1|A2", got)
	}
	if got := adapter.prompt(t, sessionB, "B2"); got != "B1|B2" {
		t.Fatalf("restored B history = %q, want B1|B2", got)
	}
	adapter.close(t)

	adapter = startAdapter(t, env)
	defer adapter.close(t)
	if got := adapter.prompt(t, sessionA, "A3"); got != "A1|A2|A3" {
		t.Fatalf("adapter-restart A history = %q, want A1|A2|A3", got)
	}
}

// TestPiACPHelperProcess is launched through the fake pi wrapper. It implements
// the small subset of PI RPC used by pi-acp during Session creation, prompting,
// and restore.
func TestPiACPHelperProcess(t *testing.T) {
	if os.Getenv(fakePIHelperEnv) != "1" {
		return
	}
	args := argsAfterDoubleDash(os.Args)
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("0.83.0")
		os.Exit(0)
	}
	if err := runFakePI(args, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

type acpAdapter struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan map[string]any
	done   chan error
	stderr bytes.Buffer
	nextID int
	mu     sync.Mutex
}

func startAdapter(t *testing.T, env []string) *acpAdapter {
	t.Helper()
	cmd := exec.Command("npx", "-y", config.PiACPPackageSpec)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	adapter := &acpAdapter{
		cmd:   cmd,
		stdin: stdin,
		lines: make(chan map[string]any, 128),
		done:  make(chan error, 1),
	}
	cmd.Stderr = &adapter.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s error = %v", config.PiACPPackageSpec, err)
	}
	go adapter.read(stdout)
	go func() { adapter.done <- cmd.Wait() }()

	adapter.request(t, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]string{"name": "lumi-integration-test", "version": "1"},
	})
	return adapter
}

func (a *acpAdapter) newSession(t *testing.T, cwd string) string {
	t.Helper()
	result, _ := a.request(t, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	sessionID, _ := result["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session/new returned no sessionId: %#v", result)
	}
	return sessionID
}

func (a *acpAdapter) prompt(t *testing.T, sessionID, message string) string {
	t.Helper()
	_, texts := a.request(t, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": message}},
	})
	for i := len(texts) - 1; i >= 0; i-- {
		if strings.Contains(texts[i], message) && !strings.HasPrefix(texts[i], "pi v") {
			return texts[i]
		}
	}
	t.Fatalf("session/prompt produced no fake history for %q; text updates=%q", message, texts)
	return ""
}

func (a *acpAdapter) request(t *testing.T, method string, params any) (map[string]any, []string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	id := a.nextID
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal(%s) error = %v", method, err)
	}
	if _, err := a.stdin.Write(append(data, '\n')); err != nil {
		t.Fatalf("write %s error = %v; stderr=%s", method, err, a.stderr.String())
	}

	texts := []string{}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case msg, ok := <-a.lines:
			if !ok {
				t.Fatalf("%s: adapter output closed; stderr=%s", method, a.stderr.String())
			}
			if text := sessionUpdateText(msg); text != "" {
				texts = append(texts, text)
			}
			if responseID(msg) != id {
				continue
			}
			if rpcErr, ok := msg["error"]; ok && rpcErr != nil {
				t.Fatalf("%s RPC error: %#v; stderr=%s", method, rpcErr, a.stderr.String())
			}
			result, _ := msg["result"].(map[string]any)
			return result, texts
		case err := <-a.done:
			t.Fatalf("%s: adapter exited: %v; stderr=%s", method, err, a.stderr.String())
		case <-timer.C:
			t.Fatalf("%s timed out; stderr=%s", method, a.stderr.String())
		}
	}
}

func (a *acpAdapter) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var msg map[string]any
		if json.Unmarshal(scanner.Bytes(), &msg) == nil {
			a.lines <- msg
		}
	}
	close(a.lines)
}

func (a *acpAdapter) close(t *testing.T) {
	t.Helper()
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return
	}
	_ = a.stdin.Close()
	select {
	case <-a.done:
	case <-time.After(5 * time.Second):
		_ = a.cmd.Process.Kill()
		select {
		case <-a.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("pi-acp did not exit; stderr=%s", a.stderr.String())
		}
	}
}

func responseID(msg map[string]any) int {
	value, ok := msg["id"].(float64)
	if !ok {
		return 0
	}
	return int(value)
}

func sessionUpdateText(msg map[string]any) string {
	if msg["method"] != "session/update" {
		return ""
	}
	params, _ := msg["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	content, _ := update["content"].(map[string]any)
	text, _ := content["text"].(string)
	return text
}

func writeFakePIWrapper(t *testing.T, binDir string) string {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	path := filepath.Join(binDir, "pi")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=TestPiACPHelperProcess -- \"$@\"\n", testBinary)
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(wrapper) error = %v", err)
	}
	return path
}

func runFakePI(args []string, stdin io.Reader, stdout io.Writer) error {
	stateDir := os.Getenv("LUMI_PI_ACP_FAKE_STATE_DIR")
	if stateDir == "" {
		return errors.New("missing fake PI state directory")
	}
	sessionFile := argumentValue(args, "--session")
	sessionID := ""
	if sessionFile != "" {
		sessionID = strings.TrimSuffix(filepath.Base(sessionFile), filepath.Ext(sessionFile))
	} else {
		sessionID = fmt.Sprintf("session-%d", os.Getpid())
		sessionFile = filepath.Join(stateDir, sessionID+".jsonl")
		if err := os.WriteFile(sessionFile, nil, 0600); err != nil {
			return err
		}
	}

	scanner := bufio.NewScanner(stdin)
	writer := bufio.NewWriter(stdout)
	for scanner.Scan() {
		var request map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		id, _ := request["id"].(string)
		typeName, _ := request["type"].(string)
		response := map[string]any{"type": "response", "id": id, "success": true}
		switch typeName {
		case "get_state":
			response["data"] = map[string]any{
				"sessionId":     sessionID,
				"sessionFile":   sessionFile,
				"model":         map[string]string{"provider": "fake", "id": "model"},
				"thinkingLevel": "off",
			}
		case "get_available_models":
			response["data"] = map[string]any{
				"models": []map[string]string{{"provider": "fake", "id": "model", "name": "Fake"}},
			}
		case "get_commands":
			response["data"] = map[string]any{"commands": []any{}}
		case "get_messages":
			response["data"] = map[string]any{"messages": []any{}}
		case "prompt":
			message, _ := request["message"].(string)
			history, err := appendFakeHistory(sessionFile, message)
			if err != nil {
				return err
			}
			if err := writeJSONLine(writer, map[string]any{
				"type":                  "message_update",
				"assistantMessageEvent": map[string]any{"type": "text_delta", "delta": strings.Join(history, "|")},
			}); err != nil {
				return err
			}
			if err := writeJSONLine(writer, map[string]any{"type": "agent_settled"}); err != nil {
				return err
			}
		}
		if err := writeJSONLine(writer, response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func appendFakeHistory(path, message string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	history := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			history = append(history, line)
		}
	}
	history = append(history, message)
	return history, os.WriteFile(path, []byte(strings.Join(history, "\n")+"\n"), 0600)
}

func writeJSONLine(writer *bufio.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := writer.Write(append(data, '\n')); err != nil {
		return err
	}
	return writer.Flush()
}

func argsAfterDoubleDash(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func argumentValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}
