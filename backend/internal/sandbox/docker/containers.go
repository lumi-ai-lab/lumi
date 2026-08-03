package docker

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/errdefs"
)

const securedRequesterContextRoot = "/run/lumi/requester-context"

type ContainerSpec struct {
	Name                      string
	Image                     string
	WorkspacePath             string
	ConfigHostPath            string
	RuntimeHostPath           string
	BackendURL                string
	Token                     string
	Labels                    map[string]string
	ExtraHosts                []string
	CredentialMounts          []CredentialMount
	RequesterContextHostPath  string
	RequesterContextRoot      string
	RequesterContextReaderGID *uint32
}

type CredentialMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

func (c *Client) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	env, err := containerEnvironment(spec)
	if err != nil {
		return "", err
	}
	mounts, err := containerMounts(spec)
	if err != nil {
		return "", err
	}

	resp, err := c.raw.ContainerCreate(
		ctx,
		&container.Config{
			Image:      spec.Image,
			WorkingDir: "/workspace",
			Labels:     spec.Labels,
			Env:        env,
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

func containerMounts(spec ContainerSpec) ([]mount.Mount, error) {
	securedRequesterContext, err := validateRequesterContextSpec(spec)
	if err != nil {
		return nil, err
	}
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
	if securedRequesterContext {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: spec.RequesterContextHostPath,
			Target: securedRequesterContextRoot,
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
	return mounts, nil
}

func containerEnvironment(spec ContainerSpec) ([]string, error) {
	env := []string{
		"LUMI_WORKSPACE_PATH=/workspace",
		"NPM_CONFIG_PREFIX=/lumi/runtime/npm",
		"NPM_CONFIG_CACHE=/lumi/runtime/npm-cache",
		"PATH=/lumi/runtime/npm/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	secure, err := validateRequesterContextSpec(spec)
	if err != nil {
		return nil, err
	}
	if !secure {
		return env, nil
	}
	return append(env,
		"LUMI_REQUESTER_CONTEXT_ROOT="+spec.RequesterContextRoot,
		"LUMI_REQUESTER_CONTEXT_READER_GID="+strconv.FormatUint(uint64(*spec.RequesterContextReaderGID), 10),
	), nil
}

func validateRequesterContextSpec(spec ContainerSpec) (bool, error) {
	hostSet := spec.RequesterContextHostPath != ""
	rootSet := spec.RequesterContextRoot != ""
	gidSet := spec.RequesterContextReaderGID != nil
	if hostSet != rootSet || rootSet != gidSet {
		return false, fmt.Errorf("sandbox requester-context host path, root and reader GID must be configured together")
	}
	if !rootSet {
		return false, nil
	}
	if spec.RequesterContextRoot != securedRequesterContextRoot {
		return false, fmt.Errorf("sandbox requester-context root must be %s", securedRequesterContextRoot)
	}
	if !filepath.IsAbs(spec.RequesterContextHostPath) || filepath.Clean(spec.RequesterContextHostPath) != spec.RequesterContextHostPath {
		return false, fmt.Errorf("sandbox requester-context host path must be clean and absolute")
	}
	if filepath.Base(spec.RequesterContextHostPath) != "requester-context" {
		return false, fmt.Errorf("sandbox requester-context host path basename must be requester-context")
	}
	if *spec.RequesterContextReaderGID == 0 {
		return false, fmt.Errorf("sandbox requester-context reader GID must not be root")
	}
	return true, nil
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
