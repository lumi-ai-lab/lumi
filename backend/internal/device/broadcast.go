package device

import (
	"context"
	"time"
)

// ConnectedDeviceIDs returns the IDs of every device with an active connection.
func (r *Registry) ConnectedDeviceIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.conns))
	for id, conn := range r.conns {
		if conn != nil {
			out = append(out, id)
		}
	}
	return out
}

// PushSSOT delivers a SSOTSyncPayload to a single device, waiting for the
// device's ack. Errors mirror SendToDevice's error semantics.
func (r *Registry) PushSSOT(ctx context.Context, deviceID string, payload SSOTSyncPayload) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return r.SendToDevice(cctx, deviceID, MsgSSOTSync, "", payload)
}

// BroadcastSSOT sends payload to every currently-connected device. It does
// not block on results; per-device errors are returned in the map (keyed by
// deviceID). Devices that are offline are silently skipped.
func (r *Registry) BroadcastSSOT(ctx context.Context, payload SSOTSyncPayload) map[string]error {
	out := map[string]error{}
	for _, id := range r.ConnectedDeviceIDs() {
		if err := r.PushSSOT(ctx, id, payload); err != nil {
			out[id] = err
		}
	}
	return out
}
