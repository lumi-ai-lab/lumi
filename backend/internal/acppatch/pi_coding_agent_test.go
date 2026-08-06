package acppatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Minimal 0.83.0 anchors used by piCodingAgentFilePatches (must stay in sync).
const (
	testRPCAnchor = `                void session
                    .prompt(command.message, {
                    images: command.images,
                    streamingBehavior: command.streamingBehavior,
                    source: "rpc",
                    preflightResult: (didSucceed) => {
                        if (didSucceed) {
                            preflightSucceeded = true;
                            output(success(id, "prompt"));
                        }
                    },
                })`
	testSessionAnchor = `    async prompt(text, options) {
        const expandPromptTemplates = options?.expandPromptTemplates ?? true;
        const preflightResult = options?.preflightResult;`
	testRunnerAnchor = `    async emitContext(messages) {
        const ctx = this.createContext();
        let currentMessages = structuredClone(messages);
        for (const ext of this.extensions) {
            const handlers = ext.handlers.get("context");
            if (!handlers || handlers.length === 0)
                continue;
            for (const handler of handlers) {
                try {
                    const event = { type: "context", messages: currentMessages };
                    const handlerResult = await handler(event, ctx);`
)

func TestAgentLooksPatchedRequiresAllThreeFiles(t *testing.T) {
	pkgDir := makeAgentPackage(t)
	if agentLooksPatched(pkgDir) {
		t.Fatal("unpatched package looks patched")
	}

	// Only runner patched (historical footgun: marker + runner short-circuited setup).
	applyOneAgentPatch(t, pkgDir, "dist/core/extensions/runner.js")
	if agentLooksPatched(pkgDir) {
		t.Fatal("runner-only package must not look fully patched")
	}

	applyOneAgentPatch(t, pkgDir, "dist/modes/rpc/rpc-mode.js")
	if agentLooksPatched(pkgDir) {
		t.Fatal("rpc+runner without agent-session must not look fully patched")
	}

	applyOneAgentPatch(t, pkgDir, "dist/core/agent-session.js")
	if !agentLooksPatched(pkgDir) {
		t.Fatal("all three files patched but agentLooksPatched=false")
	}
}

func TestEnsurePiCodingAgentRepairsIncompletePatch(t *testing.T) {
	pkgDir := makeAgentPackage(t)
	// Simulate bad prior deploy: runner patched + marker, RPC still stock.
	applyOneAgentPatch(t, pkgDir, "dist/core/extensions/runner.js")
	if err := writeAgentMarker(pkgDir); err != nil {
		t.Fatalf("writeAgentMarker: %v", err)
	}
	if agentLooksPatched(pkgDir) {
		t.Fatal("incomplete package should not look patched")
	}

	status, err := EnsurePiCodingAgentHostAuthPatched(RuntimeOptions{
		Prefix: runtimePrefixForPackageDir(pkgDir),
	})
	if err != nil {
		t.Fatalf("EnsurePiCodingAgentHostAuthPatched: %v", err)
	}
	if !status.Applied {
		t.Fatalf("status.Applied=false: %s", status.Message)
	}
	if !agentLooksPatched(pkgDir) {
		t.Fatal("after ensure, package still incomplete")
	}
	rpc, err := os.ReadFile(filepath.Join(pkgDir, "dist/modes/rpc/rpc-mode.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rpc), "hostAuth: command.hostAuth") {
		t.Fatal("rpc-mode.js still missing hostAuth pass-through after repair")
	}
}

func makeAgentPackage(t *testing.T) string {
	t.Helper()
	prefix := t.TempDir()
	pkgDir := filepath.Join(prefix, "lib", "node_modules", "@earendil-works", "pi-coding-agent")
	for _, rel := range []string{
		"dist/modes/rpc",
		"dist/core/extensions",
		"dist/core",
	} {
		if err := os.MkdirAll(filepath.Join(pkgDir, rel), 0755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", rel, err)
		}
	}
	writePackageJSON(t, pkgDir, PiCodingAgentPackage, PiCodingAgentVersion)
	files := map[string]string{
		"dist/modes/rpc/rpc-mode.js":     testRPCAnchor + "\n",
		"dist/core/agent-session.js":     testSessionAnchor + "\n",
		"dist/core/extensions/runner.js": testRunnerAnchor + "\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(pkgDir, rel), []byte(body), 0644); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
	}
	return pkgDir
}

func applyOneAgentPatch(t *testing.T, pkgDir, rel string) {
	t.Helper()
	var p *agentFilePatch
	for i := range piCodingAgentFilePatches {
		if piCodingAgentFilePatches[i].relPath == rel {
			p = &piCodingAgentFilePatches[i]
			break
		}
	}
	if p == nil {
		t.Fatalf("no patch for %s", rel)
	}
	path := filepath.Join(pkgDir, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, p.old) {
		t.Fatalf("%s missing anchor", rel)
	}
	updated := strings.Replace(text, p.old, p.new, 1)
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		t.Fatal(err)
	}
}
