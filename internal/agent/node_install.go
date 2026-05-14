package agent

import "strings"

const (
	agentNodeDefaultVersion = "22"
	agentCurlConnectTimeout = "5"
	agentCurlMaxTime        = "30"
	agentNVMInstallURL      = "https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.2/install.sh"
	agentNVMFallbackURL     = "https://gitee.com/mirrors/nvm/raw/v0.40.2/install.sh"
	agentNVMInstallSHA256   = "a909fdd01765379ebc5983674adafb8bc9de6d928bfa188761309d4a0c36be0f"
	// agentNVMSource is the git clone source nvm uses for self-updates. Points
	// to the upstream GitHub repo; users in regions with limited access can
	// override via the NVM_SOURCE environment variable (e.g.
	// https://gitee.com/mirrors/nvm.git for mainland China).
	agentNVMSource = "https://github.com/nvm-sh/nvm.git"
	// agentNodeMirror is the default mirror for Node.js tarballs used by nvm.
	// Points to the official distribution; users in regions with limited access
	// can override via the NVM_NODEJS_ORG_MIRROR environment variable
	// (e.g. https://mirrors.aliyun.com/nodejs-release for mainland China).
	agentNodeMirror = "https://nodejs.org/dist"
	// agentNPMRegistry is the default npm registry. Points to the official
	// registry; users can override via npm_config_registry or NPM_CONFIG_REGISTRY
	// (e.g. https://registry.npmmirror.com for mainland China).
	agentNPMRegistry = "https://registry.npmjs.org"
)

func nodeBootstrapLines(nodeVersion string) []string {
	if nodeVersion == "" {
		nodeVersion = agentNodeDefaultVersion
	}
	return []string{
		`export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`,
		`export NVM_SYMLINK_CURRENT=true`,
		`export NVM_SOURCE="${NVM_SOURCE:-` + agentNVMSource + `}"`,
		`export NVM_NODEJS_ORG_MIRROR="${NVM_NODEJS_ORG_MIRROR:-` + agentNodeMirror + `}"`,
		`export npm_config_registry="${npm_config_registry:-${NPM_CONFIG_REGISTRY:-` + agentNPMRegistry + `}}"`,
		`agent_npm_prefix="${npm_config_prefix:-$HOME/.local}"`,
		`unset npm_config_prefix`,
		`if [ -s "$NVM_DIR/nvm.sh" ]; then . "$NVM_DIR/nvm.sh"; fi`,
		`node_major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)"`,
		`case "$node_major" in ""|*[!0-9]*) node_major=0;; esac`,
		"if ! command -v npm >/dev/null 2>&1 || [ \"$node_major\" -lt " + shellQuote(nodeVersion) + " ]; then",
		"  if ! command -v nvm >/dev/null 2>&1; then",
		`    mkdir -p "$NVM_DIR"`,
		`    nvm_install_script="$(mktemp)"`,
		"    curl --connect-timeout " + shellQuote(agentCurlConnectTimeout) + " --max-time " + shellQuote(agentCurlMaxTime) + " -fsSL " + shellQuote(agentNVMInstallURL) + ` -o "$nvm_install_script" || curl --connect-timeout ` + shellQuote(agentCurlConnectTimeout) + " --max-time " + shellQuote(agentCurlMaxTime) + " -fsSL " + shellQuote(agentNVMFallbackURL) + ` -o "$nvm_install_script"`,
		`    if command -v sha256sum >/dev/null 2>&1; then nvm_install_sha256="$(sha256sum "$nvm_install_script" | awk '{print $1}')"; elif command -v shasum >/dev/null 2>&1; then nvm_install_sha256="$(shasum -a 256 "$nvm_install_script" | awk '{print $1}')"; else echo "sha256 checker not found" >&2; rm -f "$nvm_install_script"; exit 1; fi`,
		"    if [ \"$nvm_install_sha256\" != " + shellQuote(agentNVMInstallSHA256) + " ]; then echo \"nvm install checksum mismatch\" >&2; rm -f \"$nvm_install_script\"; exit 1; fi",
		`    bash "$nvm_install_script"`,
		`    rm -f "$nvm_install_script"`,
		`    . "$NVM_DIR/nvm.sh"`,
		"  fi",
		"  nvm install " + shellQuote(nodeVersion),
		"  nvm use " + shellQuote(nodeVersion),
		"fi",
		"if [ \"$node_major\" -lt " + shellQuote(nodeVersion) + " ] && command -v nvm >/dev/null 2>&1; then nvm use " + shellQuote(nodeVersion) + "; fi",
		`export npm_config_prefix="$agent_npm_prefix"`,
		`mkdir -p "$npm_config_prefix/bin"`,
		`export PATH="$npm_config_prefix/bin:$PATH"`,
	}
}

func nodeRuntimeCommand(command string) string {
	return nodeRuntimeCommandWithGuard("", command)
}

// nodeRuntimeCommandWithGuard wraps command with the Node/nvm bootstrap,
// but skips the bootstrap entirely when cliBin is already on PATH. This lets
// host-style runtimes (environment.type: none) reuse a locally-installed
// claude / codex without forcing an nvm switch.
func nodeRuntimeCommandWithGuard(cliBin, command string) string {
	lines := []string{"set -e"}
	bootstrap := nodeBootstrapLines(agentNodeDefaultVersion)
	if cliBin == "" {
		lines = append(lines, bootstrap...)
	} else {
		lines = append(lines, "if ! command -v "+shellQuote(cliBin)+" >/dev/null 2>&1; then")
		for _, l := range bootstrap {
			lines = append(lines, "  "+l)
		}
		lines = append(lines, "fi")
	}
	lines = append(lines, command)
	return strings.Join(lines, "\n")
}
