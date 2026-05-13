package userconfig

import (
	"reflect"
	"testing"
)

type secretFixture struct {
	Public string
	Secret string `secret:"true"`
	Nested struct {
		Token string `secret:"true"`
	}
}

func TestRedact_Tagged(t *testing.T) {
	in := secretFixture{Public: "ok", Secret: "abc"}
	in.Nested.Token = "xyz"

	out := in
	redactValue(reflect.ValueOf(&out).Elem())

	if out.Public != "ok" {
		t.Errorf("public field modified: %q", out.Public)
	}
	if out.Secret != redactMask {
		t.Errorf("secret not masked: %q", out.Secret)
	}
	if out.Nested.Token != redactMask {
		t.Errorf("nested secret not masked: %q", out.Nested.Token)
	}
}

func TestRedact_PreservesEnvRef(t *testing.T) {
	in := secretFixture{Secret: "${MY_TOKEN}"} //nolint:gosec // test fixture, not a real credential
	out := in
	redactValue(reflect.ValueOf(&out).Elem())
	if out.Secret != "${MY_TOKEN}" {
		t.Errorf("env ref should be preserved, got %q", out.Secret)
	}
}

func TestRedact_EmptyConfig(t *testing.T) {
	c := Config{Telemetry: Telemetry{ServiceName: "svc"}}
	got := Redact(c)
	if got.Telemetry.ServiceName != "svc" {
		t.Errorf("non-secret field clobbered: %q", got.Telemetry.ServiceName)
	}
}
