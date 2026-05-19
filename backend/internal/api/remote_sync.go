package api

import (
	"context"
	"encoding/json"
	"log"

	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/skillsync"
)

// broadcastRemoteSSOT pushes the current SSOT (skills + MCP records) to every
// connected device-executor. The reset flag instructs each device to drop
// lockfile-tracked entries no longer present in the payload.
func (s *Server) broadcastRemoteSSOT(ctx context.Context, reset bool) map[string]string {
	out := map[string]string{}
	if s == nil || s.devices == nil {
		return out
	}
	connected := s.devices.ConnectedDeviceIDs()
	if len(connected) == 0 {
		return out
	}
	payload, err := s.buildSSOTPayload(ctx, reset)
	if err != nil {
		out["__build"] = err.Error()
		return out
	}
	for id, err := range s.devices.BroadcastSSOT(ctx, payload) {
		if err != nil {
			out[id] = err.Error()
		}
	}
	return out
}

// pushRemoteSSOTTo pushes the SSOT to a single device id (used after a fresh
// device.register so the device starts with the current state).
func (s *Server) pushRemoteSSOTTo(ctx context.Context, deviceID string) {
	if s == nil || s.devices == nil || deviceID == "" {
		return
	}
	if s.skillStoreEmpty() && s.mcpStoreEmpty() {
		return
	}
	payload, err := s.buildSSOTPayload(ctx, true)
	if err != nil {
		log.Printf("ssot push to %s build failed: %v", deviceID, err)
		return
	}
	if err := s.devices.PushSSOT(ctx, deviceID, payload); err != nil {
		log.Printf("ssot push to %s send failed: %v", deviceID, err)
	}
}

func (s *Server) skillStoreEmpty() bool {
	return s == nil || s.skillStore == nil || len(s.skillStore.List()) == 0
}

func (s *Server) mcpStoreEmpty() bool {
	return s == nil || s.mcpStore == nil || len(s.mcpStore.List()) == 0
}

func (s *Server) buildSSOTPayload(ctx context.Context, reset bool) (device.SSOTSyncPayload, error) {
	payload := device.SSOTSyncPayload{Reset: reset}
	if s.skillStore != nil {
		blobs, errs := skillsync.BuildRemoteSkills(ctx, s.skillStore, nil)
		for _, err := range errs {
			log.Printf("ssot skill build: %v", err)
		}
		payload.Skills = make([]device.SSOTSkillBlob, 0, len(blobs))
		for _, b := range blobs {
			files := make([]device.SSOTSkillFile, 0, len(b.Files))
			for _, f := range b.Files {
				files = append(files, device.SSOTSkillFile{Path: f.Path, Content: f.Content, Mode: f.Mode})
			}
			payload.Skills = append(payload.Skills, device.SSOTSkillBlob{
				ID: b.ID, Name: b.Name, Apps: b.Apps, Files: files,
			})
		}
	}
	if s.mcpStore != nil {
		records := s.mcpStore.List()
		payload.MCP = make([]json.RawMessage, 0, len(records))
		for _, r := range records {
			data, err := json.Marshal(r)
			if err != nil {
				continue
			}
			payload.MCP = append(payload.MCP, data)
		}
	}
	return payload, nil
}
