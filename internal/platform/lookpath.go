package platform

import (
	"errors"
	"fmt"
	"os/exec"
)

// ErrBinaryNotFound reports that an executable could not be resolved on PATH.
var ErrBinaryNotFound = errors.New("executable not found on PATH")

// LookAgentBinary resolves an agent executable name to an absolute path.
//
// On Windows, exec.LookPath already honors PATHEXT (.exe/.cmd/.bat), so this
// wrapper exists to give callers a single discovery entry point and a
// normalized, wrappable not-found error.
func LookAgentBinary(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrBinaryNotFound, name)
	}
	return p, nil
}
