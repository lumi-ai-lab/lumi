//go:build !windows

package fssecure

import (
	"fmt"
	"os"
	"syscall"
)

func SetGroup(path string, gid *uint32) error {
	if gid == nil {
		return nil
	}
	return os.Chown(path, -1, int(*gid))
}

func ValidatePublisherOwnership(path string, info os.FileInfo, gid *uint32) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect managed path ownership %q: unsupported stat type %T", path, info.Sys())
	}
	if got, want := uint32(stat.Uid), uint32(os.Geteuid()); got != want {
		return fmt.Errorf("managed path %q owner UID is %d, want publisher UID %d", path, got, want)
	}
	if gid != nil && uint32(stat.Gid) != *gid {
		return fmt.Errorf("managed path %q group GID is %d, want %d", path, uint32(stat.Gid), *gid)
	}
	return nil
}
