package device

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/pengmide/lumi/internal/setupcheck"
	"nhooyr.io/websocket"
)

func (r *Registry) HandleWebSocket(w http.ResponseWriter, req *http.Request) {
	if !ValidateBearer(req.Header.Get("Authorization"), r.secret) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(WebSocketReadLimit)

	deviceConn := NewConnection(conn)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	registered := make(chan string, 1)

	go deviceConn.WriteLoop(ctx)
	go deviceConn.ReadLoop(ctx, func(env Envelope) {
		r.handleDeviceMessage(ctx, deviceConn, env, registered)
	})

	select {
	case deviceID := <-registered:
		<-deviceConn.closed
		r.MarkDisconnectedConnection(deviceID, deviceConn, "connection closed")
	case <-time.After(10 * time.Second):
		deviceConn.Close("register timeout")
	case <-ctx.Done():
	}
}

func (r *Registry) handleDeviceMessage(ctx context.Context, conn *Connection, env Envelope, registered chan<- string) {
	if env.DeviceID == "" && conn.DeviceID != "" {
		env.DeviceID = conn.DeviceID
	}

	switch env.Type {
	case MsgDeviceRegister:
		payload, err := DecodePayload[DeviceRegisterPayload](env)
		if err != nil {
			_ = conn.Send(ctx, ErrorEnvelope(env.ID, "invalid_payload", err.Error()))
			return
		}
		device, err := r.RegisterDevice(conn, payload)
		if err != nil {
			_ = conn.Send(ctx, ErrorEnvelope(env.ID, "internal_error", err.Error()))
			return
		}
		_ = conn.Send(ctx, AckEnvelope(env.ID))
		select {
		case registered <- device.ID:
		default:
		}

	case MsgSetupStatus:
		payload, err := DecodePayload[setupcheck.SetupStatus](env)
		if err != nil {
			_ = conn.Send(ctx, ErrorEnvelope(env.ID, "invalid_payload", err.Error()))
			return
		}
		log.Printf("device setup.status received: deviceID=%s ready=%v environment=%d agents=%d acpPackages=%d", env.DeviceID, payload.Ready, len(payload.Environment), len(payload.Agents), len(payload.ACPPackages))
		if err := r.UpdateSetupStatus(env.DeviceID, payload); err != nil {
			_ = conn.Send(ctx, ErrorEnvelope(env.ID, "internal_error", err.Error()))
			return
		}
		_ = conn.Send(ctx, AckEnvelope(env.ID))

	case MsgDeviceStatus:
		payload, err := DecodePayload[DeviceStatusPayload](env)
		if err != nil {
			_ = conn.Send(ctx, ErrorEnvelope(env.ID, "invalid_payload", err.Error()))
			return
		}
		if err := r.UpdateDeviceStatus(env.DeviceID, payload.Status); err != nil {
			_ = conn.Send(ctx, ErrorEnvelope(env.ID, "internal_error", err.Error()))
			return
		}
		_ = conn.Send(ctx, AckEnvelope(env.ID))

	case MsgDeviceHeartbeat:
		payload, _ := DecodePayload[DeviceHeartbeatPayload](env)
		_ = r.Heartbeat(env.DeviceID, payload.RunningTaskIDs)

	case MsgTaskSession, MsgTaskEvent, MsgTaskDone, MsgTaskError, MsgPermissionRequest:
		_ = conn.Send(ctx, AckEnvelope(env.ID))
		r.forwardTaskEvent(env)

	default:
		_ = conn.Send(ctx, ErrorEnvelope(env.ID, "invalid_payload", "unsupported message type"))
	}
}
