//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	bin, cleanup := mustCompile()
	defer cleanup()
	binaryPath = bin

	os.Exit(m.Run())
}

func mustCompile() (string, func()) {
	dir, err := os.MkdirTemp("", "skill-up-e2e-*")
	if err != nil {
		panic("creating temp dir: " + err.Error())
	}

	binName := "skill-up"
	if runtime.GOOS == "windows" {
		// Windows refuses to execute a file without a recognized extension,
		// so go build's -o output must end in .exe or every later
		// exec.Command(binaryPath) fails with "executable file not found".
		binName += ".exe"
	}
	binPath := filepath.Join(dir, binName)

	_, testFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Dir(filepath.Dir(testFile))

	cmd := exec.Command("go", "build", "-o", binPath, filepath.Join(projectRoot, "cmd", "skill-up"))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("building binary: " + err.Error())
	}

	return binPath, func() { _ = os.RemoveAll(dir) }
}
