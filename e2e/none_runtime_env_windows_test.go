//go:build e2e && windows

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/platform"
	evalruntime "github.com/alibaba/skill-up/internal/runtime"
)

// windowsEnvProbeRuns repeats each child-environment probe. Before key
// folding, the spelling that won followed Go map iteration order, so a single
// run could pass by luck.
const windowsEnvProbeRuns = 10

// TestWindowsNoneRuntime_EnvKeysFoldAcrossLayers checks that process,
// runtime-config and per-call values for one variable keep their precedence
// when each layer spells the name with different case.
func TestWindowsNoneRuntime_EnvKeysFoldAcrossLayers(t *testing.T) {
	const name = "SKILL_UP_ENV_PROBE"
	t.Setenv("Skill_Up_Env_Probe", "process")

	rt := newWindowsEnvRuntime(t, map[string]string{name: "config"})

	requireChildEnvValue(t, rt, nil, name, "config")
	requireChildEnvValue(t, rt, map[string]string{"skill_up_env_probe": "call"}, name, "call")

	rt.MergeEnv(map[string]string{"Skill_Up_ENV_Probe": "merged"})
	requireChildEnvValue(t, rt, nil, name, "merged")
	requireChildEnvValue(t, rt, map[string]string{"skill_up_env_probe": "call"}, name, "call")
}

// TestWindowsNoneRuntime_PathOverrideReplacesHostSpelling mirrors the agent
// PATH probe, which merges a resolved PATH into the runtime baseline. The
// host may spell the variable Path or PATH, so the override uses the other
// spelling and must still be the PATH the child sees.
func TestWindowsNoneRuntime_PathOverrideReplacesHostSpelling(t *testing.T) {
	hostKey, hostValue := hostEnvEntry(t, "PATH")
	overrideKey := "PATH"
	if hostKey == overrideKey {
		overrideKey = "Path"
	}
	const markerName = "skill-up-env-path-marker"
	marker := filepath.Join(t.TempDir(), markerName)
	if err := os.Mkdir(marker, 0o750); err != nil {
		t.Fatal(err)
	}

	rt := newWindowsEnvRuntime(t, nil)
	rt.MergeEnv(map[string]string{overrideKey: marker + string(os.PathListSeparator) + hostValue})

	for range windowsEnvProbeRuns {
		values := childEnvValues(t, rt, nil, "PATH")
		if len(values) != 1 || !strings.Contains(values[0], markerName) {
			t.Fatalf("child PATH entries = %q, want one entry containing %q (host spelling %q, override spelling %q)", values, markerName, hostKey, overrideKey)
		}
	}
}

func newWindowsEnvRuntime(t *testing.T, env map[string]string) evalruntime.Runtime {
	t.Helper()
	rt, err := evalruntime.NewRuntime(evalruntime.Config{Type: "none", Delete: true, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func requireChildEnvValue(t *testing.T, rt evalruntime.Runtime, callEnv map[string]string, name, want string) {
	t.Helper()
	for range windowsEnvProbeRuns {
		values := childEnvValues(t, rt, callEnv, name)
		if len(values) != 1 || values[0] != want {
			t.Fatalf("child values for %s = %q, want exactly [%q]", name, values, want)
		}
	}
}

// childEnvValues runs the shell's environment listing in the runtime and
// returns the value of every entry whose name matches name ignoring case.
func childEnvValues(t *testing.T, rt evalruntime.Runtime, callEnv map[string]string, name string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := "env"
	if rt.Shell().Family == platform.ShellCmd {
		command = "set"
	}
	res, err := rt.Exec(ctx, command, evalruntime.ExecOptions{Env: callEnv})
	if err != nil {
		t.Fatalf("Exec %q: %v (stderr: %s)", command, err, res.Stderr)
	}

	var values []string
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if ok && strings.EqualFold(key, name) {
			values = append(values, value)
		}
	}
	return values
}

// hostEnvEntry returns the spelling and value the test process holds for name.
func hostEnvEntry(t *testing.T, name string) (key, value string) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, name) {
			return key, value
		}
	}
	t.Fatalf("host environment has no %s entry", name)
	return "", ""
}
