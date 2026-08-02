package docker

import "testing"

func TestContainerMountsKeepsExecutorConfigWritable(t *testing.T) {
	mounts := containerMounts(ContainerSpec{
		WorkspacePath:   "/host/workspace",
		ConfigHostPath:  "/host/config.json",
		RuntimeHostPath: "/host/runtime",
		CredentialMounts: []CredentialMount{
			{Source: "/host/pi", Target: "/root/.pi", ReadOnly: true},
		},
	})

	for _, item := range mounts {
		switch item.Target {
		case "/lumi/device-executor/config.json":
			if item.ReadOnly {
				t.Fatal("executor config mount is read-only, want writable for setup normalization")
			}
			return
		}
	}
	t.Fatal("executor config mount not found")
}
