package agent

// UnsupportedAgentError is returned when an unknown agent type is requested.
type UnsupportedAgentError struct {
	Name string
}

func (e *UnsupportedAgentError) Error() string {
	return "unsupported agent: " + e.Name
}
