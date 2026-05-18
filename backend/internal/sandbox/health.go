package sandbox

import (
	"context"
	"time"

	"github.com/pengmide/lumi/internal/device"
)

const defaultRegistrationTimeout = 10 * time.Minute

func (m *Manager) runtimeHealthy(ctx context.Context, record RuntimeRecord) bool {
	if record.ContainerName == "" || record.DeviceID == "" {
		return false
	}

	inspect, err := m.docker.InspectContainer(ctx, record.ContainerName)
	if err != nil {
		return false
	}
	if inspect.State == nil || !inspect.State.Running {
		return false
	}

	dev, ok := m.devices.GetDevice(record.DeviceID)
	if !ok {
		return false
	}
	return dev.SetupReady && dev.Status != device.StatusOffline
}

func (m *Manager) waitForDevice(ctx context.Context, deviceID string) *RuntimeError {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(defaultRegistrationTimeout)
	defer timeout.Stop()

	for {
		if dev, ok := m.devices.GetDevice(deviceID); ok && dev.SetupReady && dev.Status != device.StatusOffline {
			return nil
		}

		select {
		case <-ctx.Done():
			return wrapRuntimeError(CodeUnknown, ctx.Err())
		case <-timeout.C:
			return errorForCode(CodeExecutorRegistrationTimeout, "hidden device did not become ready before timeout")
		case <-ticker.C:
		}
	}
}
