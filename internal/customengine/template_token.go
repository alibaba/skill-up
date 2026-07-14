package customengine

import (
	"errors"
	"strings"
)

// TemplateToken is the parsed body of a ${...} reference. It is shared by the
// config-load-time resolver and the agent-runtime resolver so the
// ${X} / ${X:-default} / ${X?msg} grammar has a single source of truth.
type TemplateToken struct {
	// Name is the variable name (the part before any :- or ? clause).
	Name string
	// Default is the fallback value of a ${X:-default} form.
	Default string
	// ErrMsg is the message of a ${X?msg} form (may be empty).
	ErrMsg string
	// HasDefault is set for the ${X:-default} form.
	HasDefault bool
	// HasErrForm is set for the ${X?msg} form.
	HasErrForm bool
}

// ParseTemplateToken splits a reference body into its variable name plus an
// optional default-value or error-message clause. `:-` is matched before `?`,
// so ${X:-a?b} keeps "a?b" as the default.
func ParseTemplateToken(inner string) TemplateToken {
	t := TemplateToken{Name: inner}
	if before, after, found := strings.Cut(inner, ":-"); found {
		t.Name, t.Default, t.HasDefault = before, after, true
	} else if before, after, found := strings.Cut(inner, "?"); found {
		t.Name, t.ErrMsg, t.HasErrForm = before, after, true
	}
	return t
}

// RequiredErr builds the error for the ${X?msg} form, defaulting a blank or
// whitespace-only message to "<name> is required".
func (t TemplateToken) RequiredErr() error {
	msg := t.ErrMsg
	if strings.TrimSpace(msg) == "" {
		msg = t.Name + " is required"
	}
	return errors.New(msg)
}
