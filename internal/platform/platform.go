// Package platform centralizes OS-conditional process, shell, and executable
// discovery so the rest of skill-up does not scatter runtime.GOOS branches
// across packages.
package platform

// BashEnvOverride is the environment variable a user may set to point at a
// specific bash interpreter, taking precedence over PATH and well-known
// install locations.
const BashEnvOverride = "SKILL_UP_BASH"
