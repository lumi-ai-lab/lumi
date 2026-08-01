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

	"github.com/pengmide/lumi/internal/piacpbridge"
	"github.com/pengmide/lumi/internal/sessioninstruction"
)

const fakePIHelperEnv = "LUMI_PI_ACP_FAKE_HELPER"

func TestPiACPLogicalSessionsRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the embedded PI ACP bridge and fake PI subprocesses")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	stateDir := filepath.Join(root, "fake-pi-sessions")
	binDir := filepath.Join(root, "bin")
	observationDir := filepath.Join(stateDir, "observations")
	t.Setenv("LUMI_HOME", filepath.Join(root, "lumi-home"))
	for _, dir := range []string{home, stateDir, binDir, observationDir} {
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

	profileA := sessioninstruction.NewProfile("PRIVATE-SYSTEM-INSTRUCTION-V1", "group A context")
	profileB := sessioninstruction.NewProfile("PRIVATE-SYSTEM-INSTRUCTION-V1", "group B context")
	profileAChanged := sessioninstruction.NewProfile("PRIVATE-SYSTEM-INSTRUCTION-V2", "group A context")

	adapter := startAdapter(t, env)
	sessionA := adapter.newSession(t, root, profileA)
	if got := adapter.prompt(t, sessionA, "A1", profileA); got != "A1" {
		t.Fatalf("A1 history = %q, want A1", got)
	}

	sessionB := adapter.newSession(t, root, profileB)
	if got := adapter.prompt(t, sessionB, "B1", profileB); got != "B1" {
		t.Fatalf("B1 history = %q, want B1", got)
	}
	if sessionA == sessionB {
		t.Fatalf("session IDs are equal: %s", sessionA)
	}

	if got := adapter.prompt(t, sessionA, "A2", profileA); got != "A1|A2" {
		t.Fatalf("restored A history = %q, want A1|A2", got)
	}
	if got := adapter.prompt(t, sessionB, "B2", profileB); got != "B1|B2" {
		t.Fatalf("restored B history = %q, want B1|B2", got)
	}

	adapter.loadSession(t, sessionA, root, profileA)
	if got := adapter.prompt(t, sessionA, "A3", profileA); got != "A1|A2|A3" {
		t.Fatalf("explicitly loaded A history = %q, want A1|A2|A3", got)
	}
	if got := adapter.prompt(t, sessionA, "A4", profileAChanged); got != "A1|A2|A3|A4" {
		t.Fatalf("digest-changed A history = %q, want A1|A2|A3|A4", got)
	}
	adapter.close(t)

	adapter = startAdapter(t, env)
	defer adapter.close(t)
	if got := adapter.prompt(t, sessionB, "B3", profileB); got != "B1|B2|B3" {
		t.Fatalf("adapter-restart B history = %q, want B1|B2|B3", got)
	}

	assertInstructionTransport(t, observationDir, []sessioninstruction.Profile{profileA, profileB, profileAChanged})
	assertSessionStoreContainsDigestsOnly(t, home, []sessioninstruction.Profile{profileAChanged, profileB})
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
	entrypoint, err := piacpbridge.Materialize()
	if err != nil {
		t.Fatalf("materialize embedded PI ACP bridge: %v", err)
	}
	cmd := exec.Command("node", entrypoint)
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
		t.Fatalf("start embedded PI ACP bridge error = %v", err)
	}
	go adapter.read(stdout)
	go func() { adapter.done <- cmd.Wait() }()

	result, _ := adapter.request(t, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]string{"name": "lumi-integration-test", "version": "1"},
	})
	meta, _ := result["_meta"].(map[string]any)
	lumi, _ := meta["lumi"].(map[string]any)
	capability, _ := lumi["sessionInstructions"].(map[string]any)
	if capability["systemPromptAppend"] != true || capability["rehydrateOnRestore"] != true || capability["transportVersion"] != float64(1) {
		t.Fatalf("initialize capability = %#v", capability)
	}
	return adapter
}

func (a *acpAdapter) newSession(t *testing.T, cwd string, profile sessioninstruction.Profile) string {
	t.Helper()
	params := map[string]any{"cwd": cwd, "mcpServers": []any{}}
	applyProfile(t, params, profile, sessioninstruction.PhaseNew)
	result, _ := a.request(t, "session/new", params)
	sessionID, _ := result["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session/new returned no sessionId: %#v", result)
	}
	return sessionID
}

func (a *acpAdapter) loadSession(t *testing.T, sessionID, cwd string, profile sessioninstruction.Profile) {
	t.Helper()
	params := map[string]any{"sessionId": sessionID, "cwd": cwd, "mcpServers": []any{}}
	applyProfile(t, params, profile, sessioninstruction.PhaseLoad)
	a.request(t, "session/load", params)
}

func (a *acpAdapter) prompt(t *testing.T, sessionID, message string, profile sessioninstruction.Profile) string {
	t.Helper()
	params := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": message}},
	}
	applyProfile(t, params, profile, sessioninstruction.PhasePrompt)
	_, texts := a.request(t, "session/prompt", params)
	for i := len(texts) - 1; i >= 0; i-- {
		if strings.Contains(texts[i], message) && !strings.HasPrefix(texts[i], "pi v") {
			return texts[i]
		}
	}
	t.Fatalf("session/prompt produced no fake history for %q; text updates=%q", message, texts)
	return ""
}

func applyProfile(t *testing.T, params map[string]any, profile sessioninstruction.Profile, phase sessioninstruction.Phase) {
	t.Helper()
	if err := sessioninstruction.ApplyProfile(params, sessioninstruction.Support{
		Transport: sessioninstruction.TransportLumiV1,
		Capability: sessioninstruction.Capability{
			TransportVersion:   sessioninstruction.TransportVersion,
			SystemPromptAppend: true,
			RehydrateOnRestore: true,
		},
	}, profile, phase); err != nil {
		t.Fatal(err)
	}
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
	if err := recordInstructionObservation(stateDir, args); err != nil {
		return err
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

type instructionObservation struct {
	InstructionPath string   `json:"instructionPath"`
	Mode            uint32   `json:"mode"`
	Argv            []string `json:"argv"`
	Body            string   `json:"body"`
}

func recordInstructionObservation(stateDir string, args []string) error {
	instructionPath := argumentValue(args, "--append-system-prompt")
	if instructionPath == "" {
		return errors.New("fake PI started without --append-system-prompt")
	}
	info, err := os.Stat(instructionPath)
	if err != nil {
		return fmt.Errorf("stat system instruction file: %w", err)
	}
	body, err := os.ReadFile(instructionPath)
	if err != nil {
		return fmt.Errorf("read system instruction file: %w", err)
	}
	observation := instructionObservation{
		InstructionPath: instructionPath,
		Mode:            uint32(info.Mode().Perm()),
		Argv:            append([]string(nil), args...),
		Body:            string(body),
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, "observations", fmt.Sprintf("%d.json", os.Getpid()))
	return os.WriteFile(path, encoded, 0600)
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

func assertInstructionTransport(t *testing.T, observationDir string, profiles []sessioninstruction.Profile) {
	t.Helper()
	entries, err := os.ReadDir(observationDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 7 {
		t.Fatalf("PI spawn observations = %d, want at least 7", len(entries))
	}
	wantedBodies := make(map[string]string, len(profiles))
	observedBodies := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		wantedBodies[profile.Text()] = profile.ProfileDigest
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(observationDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var observation instructionObservation
		if err := json.Unmarshal(data, &observation); err != nil {
			t.Fatalf("decode %s: %v", entry.Name(), err)
		}
		if observation.Mode != 0600 {
			t.Fatalf("system instruction mode = %o, want 600", observation.Mode)
		}
		if _, ok := wantedBodies[observation.Body]; !ok {
			t.Fatalf("PI received unexpected system instruction body")
		}
		observedBodies[observation.Body] = true
		for _, arg := range observation.Argv {
			if strings.Contains(arg, "PRIVATE-SYSTEM-INSTRUCTION") || strings.Contains(arg, "group A context") || strings.Contains(arg, "group B context") {
				t.Fatalf("PI argv contains instruction text")
			}
		}
		if _, err := os.Stat(observation.InstructionPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary instruction file still exists: %v", err)
		}
		if _, err := os.Stat(filepath.Dir(observation.InstructionPath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary instruction directory still exists: %v", err)
		}
	}
	for body, digest := range wantedBodies {
		if !observedBodies[body] {
			t.Fatalf("PI never received expected instruction profile with digest %s", digest[:12])
		}
	}
}

func assertSessionStoreContainsDigestsOnly(t *testing.T, home string, profiles []sessioninstruction.Profile) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".pi", "pi-acp", "session-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, secret := range []string{"PRIVATE-SYSTEM-INSTRUCTION", "group A context", "group B context"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("SessionStore persisted instruction text")
		}
	}
	for _, profile := range profiles {
		if !strings.Contains(serialized, profile.ProfileDigest) {
			t.Fatalf("SessionStore missing profile digest %s", profile.ProfileDigest[:12])
		}
	}
}
