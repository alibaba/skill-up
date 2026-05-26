package agent

import "fmt"

// UnsupportedAgentError is returned when an engine name matches no built-in
// agent and no engine.custom configuration was supplied.
type UnsupportedAgentError struct {
	Name string
}

func (e *UnsupportedAgentError) Error() string {
	return fmt.Sprintf("unsupported agent %q: missing engine.custom", e.Name)
}
