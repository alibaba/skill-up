package agent

import (
	"bytes"
	"sync"
	"testing"

	"github.com/alibaba/skill-up/internal/logging"
)

var logCaptureMu sync.Mutex

// captureStdout redirects package-level log output for agent tests.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	var buf bytes.Buffer
	restoreOutput := logging.SetOutputForTest(&buf)

	fn()

	restoreOutput()
	return buf.String()
}
