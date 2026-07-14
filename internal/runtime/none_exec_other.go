//go:build !unix

package runtime

import "os/exec"

// configureProcessGroup is a no-op on platforms without POSIX process groups.
func configureProcessGroup(_ *exec.Cmd, _ <-chan struct{}) {}
