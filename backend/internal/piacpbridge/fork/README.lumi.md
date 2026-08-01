# Lumi PI ACP bridge

This directory is a source-auditable thin fork of
[`pi-acp@0.0.33`](https://github.com/svkozak/pi-acp/releases/tag/v0.0.33),
upstream commit `1bfcb394088ed879db8fd936b570bb626017f878`.

Lumi carries the fork temporarily because ACP does not standardize system
prompt injection and upstream `pi-acp` does not consume Lumi's session
instruction metadata. The Lumi delta is deliberately limited to:

- an explicit namespaced capability returned from `initialize`;
- validated session instruction profiles on `session/new`, `session/load`, and
  `session/prompt`;
- PI's native `--append-system-prompt <0600-temp-file>` transport;
- profile-digest-only persistence and profile-aware Session restore;
- untrusted turn context kept at user-message priority;
- contract and security tests for those behaviors.

The compiled adapter bundles its npm runtime dependencies and is embedded into
the Lumi/device-executor Go binaries. Lumi does not patch an installed npm
package and does not log or persist instruction bodies in the adapter mapping.
The bridge license and bundled dependency notices are retained in `LICENSE`
and `THIRD_PARTY_NOTICES.md`, and are materialized beside the embedded bundle.

When upstream gains an equivalent reviewed transport, replace this fork with a
pinned upstream release after the same contract tests pass.
