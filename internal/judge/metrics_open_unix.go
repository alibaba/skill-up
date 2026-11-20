//go:build unix

package judge

import (
	"os"
	"syscall"
)

// Refuse a symlink at open time and avoid waiting on special files.
func openMetricsResult(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
