package agent

import (
	"strings"
	"testing"
)

func TestNodeBootstrapLines(t *testing.T) {
	t.Parallel()

	cmd := strings.Join(nodeBootstrapLines("20"), "\n")
	for _, want := range []string{
		`NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`,
		`NVM_SYMLINK_CURRENT=true`,
		`NVM_SOURCE="${NVM_SOURCE:-` + agentNVMSource + `}"`,
		`NVM_NODEJS_ORG_MIRROR="${NVM_NODEJS_ORG_MIRROR:-` + agentNodeMirror + `}"`,
		`npm_config_registry="${npm_config_registry:-${NPM_CONFIG_REGISTRY:-` + agentNPMRegistry + `}}"`,
		`agent_npm_prefix="${npm_config_prefix:-$HOME/.local}"`,
		`unset npm_config_prefix`,
		`export npm_config_prefix="$agent_npm_prefix"`,
		`export PATH="$npm_config_prefix/bin:$PATH"`,
		`if [ -s "$NVM_DIR/nvm.sh" ]; then . "$NVM_DIR/nvm.sh"; fi`,
		`node_major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)"`,
		`[ "$node_major" -lt '20' ]`,
		"nvm install '20'",
		"nvm use '20'",
		"curl --connect-timeout '" + agentCurlConnectTimeout + "' --max-time '" + agentCurlMaxTime + "' -fsSL '" + agentNVMInstallURL + `' -o "$nvm_install_script" || curl --connect-timeout '` + agentCurlConnectTimeout + "' --max-time '" + agentCurlMaxTime + "' -fsSL '" + agentNVMFallbackURL + `' -o "$nvm_install_script"`,
		`command -v sha256sum`,
		`shasum -a 256 "$nvm_install_script"`,
		`[ "$nvm_install_sha256" != '` + agentNVMInstallSHA256 + `' ]`,
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("node bootstrap command missing %q:\n%s", want, cmd)
		}
	}
}

func TestNodeBootstrapLinesUsesDefaultVersion(t *testing.T) {
	t.Parallel()

	cmd := strings.Join(nodeBootstrapLines(""), "\n")
	if !strings.Contains(cmd, "nvm install '"+agentNodeDefaultVersion+"'") {
		t.Fatalf("node bootstrap command does not use default version %s:\n%s", agentNodeDefaultVersion, cmd)
	}
}
