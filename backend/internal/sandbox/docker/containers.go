package docker

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/errdefs"
)

type ContainerSpec struct {
	Name             string
	Image            string
	WorkspacePath    string
	ConfigHostPath   string
	RuntimeHostPath  string
	BackendURL       string
	Token            string
	Labels           map[string]string
	ExtraHosts       []string
	CredentialMounts []CredentialMount
}

type CredentialMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

func (c *Client) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	mounts := []mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: spec.WorkspacePath,
			Target: "/workspace",
		},
		{
			Type:     mount.TypeBind,
			Source:   spec.ConfigHostPath,
			Target:   "/lumi/device-executor/config.json",
			ReadOnly: true,
		},
	}
	if spec.RuntimeHostPath != "" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: spec.RuntimeHostPath,
			Target: "/lumi/runtime",
		})
	}
	for _, credentialMount := range spec.CredentialMounts {
		if credentialMount.Source == "" || credentialMount.Target == "" {
			continue
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   credentialMount.Source,
			Target:   credentialMount.Target,
			ReadOnly: credentialMount.ReadOnly,
		})
	}

	resp, err := c.raw.ContainerCreate(
		ctx,
		&container.Config{
			Image:      spec.Image,
			WorkingDir: "/workspace",
			Labels:     spec.Labels,
			Env: []string{
				"NPM_CONFIG_PREFIX=/lumi/runtime/npm",
				"NPM_CONFIG_CACHE=/lumi/runtime/npm-cache",
				"PATH=/lumi/runtime/npm/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			},
			Cmd: []string{
				"connect",
				"--server", spec.BackendURL,
				"--token", spec.Token,
				"--config", "/lumi/device-executor/config.json",
				"--install",
			},
		},
		&container.HostConfig{
			Mounts:     mounts,
			ExtraHosts: spec.ExtraHosts,
		},
		nil,
		nil,
		spec.Name,
	)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	return c.raw.ContainerStart(ctx, containerID, container.StartOptions{})
}

func (c *Client) InspectContainer(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	return c.raw.ContainerInspect(ctx, containerID)
}

func (c *Client) ListSandboxContainers(ctx context.Context) ([]types.Container, error) {
	return c.raw.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: SandboxFilters(),
	})
}

func (c *Client) StopRemoveContainer(ctx context.Context, containerID string) error {
	timeout := 5
	if err := c.raw.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if err := c.raw.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}
