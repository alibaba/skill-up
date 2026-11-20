//go:build !unix && !windows

package judge

import (
	"errors"
	"os"
)

// This platform has no configured safe metrics-file opener.
func openMetricsResult(string) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
