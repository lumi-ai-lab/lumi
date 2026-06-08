# pi-acp 0.0.27 multi-session patch

Reason:
pi-acp@0.0.27 closes all existing sessions after session/new or session/load.
This breaks Lumi IM conversations because Lumi keeps one remote ACP sessionId per
conversation.

Patch:
Only call closeAllExcept(sessionId) when PI_ACP_SINGLE_LIVE_SESSION=true.
By default, pi-acp keeps multiple live sessions in the same ACP process.

Applies to:
pi-acp@0.0.27 from the npm package tarball.

Generated with:

```bash
npm pack pi-acp@0.0.27
tar -xzf pi-acp-0.0.27.tgz -C original --strip-components=1
cp -R original/. patched/
# edit patched/dist/index.js
diff -ruN original patched > multi-session.patch
```

Verify with:

```bash
patch --dry-run -p1 -d original < multi-session.patch
```

Remove when:
Upstream pi-acp supports multiple live sessions by default, or provides an
equivalent config option and Lumi upgrades its default PI ACP version.
