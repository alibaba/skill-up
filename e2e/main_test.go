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

	binPath := filepath.Join(dir, "skill-up")

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
