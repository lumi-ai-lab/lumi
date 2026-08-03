//go:build windows

package fssecure

import (
	"fmt"
	"os"
)

func SetGroup(_ string, gid *uint32) error {
	if gid != nil {
		return fmt.Errorf("numeric group ownership is not supported on Windows")
	}
	return nil
}

func ValidatePublisherOwnership(_ string, _ os.FileInfo, gid *uint32) error {
	if gid != nil {
		return fmt.Errorf("numeric group ownership is not supported on Windows")
	}
	return nil
}
