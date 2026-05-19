package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/mcpstore"
	"github.com/pengmide/lumi/internal/mcpsync"
	"github.com/pengmide/lumi/internal/skillsync"
)

// handleSSOTSync writes the pushed SSOT state into the executor host's user
// configuration directories: ~/.claude/skills, ~/.codex/skills, ~/.qwen/skills,
// plus ~/.claude.json, ~/.codex/config.toml, and ~/.qwen/settings.json. The
// payload's Reset flag, when true, removes lockfile-tracked entries that
// are not present in the new payload before applying.
func (c *Client) handleSSOTSync(_ context.Context, env Envelope) {
	payload, err := decodePayload[device.SSOTSyncPayload](env)
	if err != nil {
		c.sendSSOTAck(env, false, []string{err.Error()})
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		c.sendSSOTAck(env, false, []string{err.Error()})
		return
	}

	var errs []string

	if errsApplied := applySSOTSkillBlobs(home, payload); errsApplied != nil {
		errs = append(errs, errsApplied...)
	}
	if applyErrs := applySSOTMCPRecords(home, payload); applyErrs != nil {
		errs = append(errs, applyErrs...)
	}

	c.sendSSOTAck(env, len(errs) == 0, errs)
}

func applySSOTSkillBlobs(home string, payload device.SSOTSyncPayload) []string {
	var errs []string
	for _, backend := range skillsync.SupportedBackends() {
		appKey := skillsync.AppKey(backend)
		appDir, err := skillsync.UserSkillDir(home, backend)
		if err != nil || appDir == "" {
			continue
		}
		// Reset removes existing lockfile-tracked entries first.
		if payload.Reset {
			keep := map[string]struct{}{}
			for _, blob := range payload.Skills {
				if blob.Apps[appKey] {
					keep[blob.ID] = struct{}{}
				}
			}
			lf, lferr := skillsync.LoadLockfile(appDir)
			if lferr == nil && len(lf.Entries) > 0 {
				toRemove := map[string]struct{}{}
				for _, entry := range lf.Entries {
					if _, kept := keep[entry.ID]; !kept {
						toRemove[entry.ID] = struct{}{}
					}
				}
				if len(toRemove) > 0 {
					if rerr := skillsync.RemoveRemoteSkillsByID(appDir, toRemove); rerr != nil {
						errs = append(errs, "remove("+appKey+"): "+rerr.Error())
					}
				}
			}
		}
		for _, blob := range payload.Skills {
			if !blob.Apps[appKey] {
				continue
			}
			files := make([]skillsync.RemoteFile, 0, len(blob.Files))
			for _, f := range blob.Files {
				files = append(files, skillsync.RemoteFile{Path: f.Path, Content: f.Content, Mode: f.Mode})
			}
			rs := skillsync.RemoteSkill{ID: blob.ID, Name: blob.Name, Apps: blob.Apps, Files: files}
			if err := skillsync.WriteRemoteSkill(appDir, rs); err != nil {
				errs = append(errs, blob.Name+"("+appKey+"): "+err.Error())
			}
		}
	}
	return errs
}

func applySSOTMCPRecords(home string, payload device.SSOTSyncPayload) []string {
	var errs []string
	records := make([]mcpstore.Record, 0, len(payload.MCP))
	for _, raw := range payload.MCP {
		var r mcpstore.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			errs = append(errs, "mcp decode: "+err.Error())
			continue
		}
		records = append(records, r)
	}
	SetMCPRecords(records)
	if err := mcpsync.ApplyClaude(home, records); err != nil {
		errs = append(errs, "claude: "+err.Error())
	}
	if err := mcpsync.ApplyCodex(home, records); err != nil {
		errs = append(errs, "codex: "+err.Error())
	}
	if err := mcpsync.ApplyQwen(home, records); err != nil {
		errs = append(errs, "qwen: "+err.Error())
	}
	return errs
}

func (c *Client) sendSSOTAck(env Envelope, ok bool, errs []string) {
	payload := device.SSOTSyncAckPayload{OK: ok, Errors: errs}
	if err := c.Send(MsgSSOTSyncAck, env.TaskID, payload); err != nil {
		log.Printf("SSOT ack send failed: %v", err)
	}
}
