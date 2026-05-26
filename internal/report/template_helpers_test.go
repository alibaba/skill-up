package report

import (
	"html/template"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
)

func TestSharedTemplateFuncs(t *testing.T) {
	funcs := SharedTemplateFuncs()

	fmtDuration := requireTemplateFunc[func(int64) string](t, funcs, "fmtDuration")
	fmtPercent := requireTemplateFunc[func(float64) string](t, funcs, "fmtPercent")
	fmtPercentSigned := requireTemplateFunc[func(float64) string](t, funcs, "fmtPercentSigned")
	passFailClass := requireTemplateFunc[func(bool) string](t, funcs, "passFailClass")
	passFailIcon := requireTemplateFunc[func(bool) template.HTML](t, funcs, "passFailIcon")
	notNil := requireTemplateFunc[func(any) bool](t, funcs, "notNil")
	derefFloat := requireTemplateFunc[func(*float64) float64](t, funcs, "derefFloat")

	if got := fmtDuration(1234); got != "1.2s" {
		t.Fatalf("fmtDuration = %q, want 1.2s", got)
	}
	if got := fmtPercent(0.875); got != "88%" {
		t.Fatalf("fmtPercent = %q, want 88%%", got)
	}
	if got := fmtPercentSigned(-0.125); got != "-12%" {
		t.Fatalf("fmtPercentSigned = %q, want -12%%", got)
	}
	if got := passFailClass(true); got != "pass" {
		t.Fatalf("passFailClass(true) = %q, want pass", got)
	}
	if got := passFailClass(false); got != "fail" {
		t.Fatalf("passFailClass(false) = %q, want fail", got)
	}
	if got := passFailIcon(true); got != template.HTML("&#x2705;") {
		t.Fatalf("passFailIcon(true) = %q, want check mark entity", got)
	}
	if got := passFailIcon(false); got != template.HTML("&#x274C;") {
		t.Fatalf("passFailIcon(false) = %q, want cross mark entity", got)
	}

	if notNil(nil) {
		t.Fatal("notNil(nil) = true, want false")
	}
	var ptr *int
	if notNil(ptr) {
		t.Fatal("notNil(nil pointer) = true, want false")
	}
	if !notNil(0) || !notNil([]string{"x"}) {
		t.Fatal("notNil should treat non-nil scalar and slice as true")
	}

	if got := derefFloat(nil); got != 0 {
		t.Fatalf("derefFloat(nil) = %f, want 0", got)
	}
	value := 0.42
	if got := derefFloat(&value); got != value {
		t.Fatalf("derefFloat(&value) = %f, want %f", got, value)
	}
}

func requireTemplateFunc[T any](t *testing.T, funcs template.FuncMap, name string) T {
	t.Helper()
	fn, ok := funcs[name].(T)
	if !ok {
		t.Fatalf("template func %s has unexpected type %T", name, funcs[name])
	}
	return fn
}

func TestStatusIcon(t *testing.T) {
	tests := map[judge.Status]template.HTML{
		judge.StatusPass:  "&#x2705;",
		judge.StatusFail:  "&#x274C;",
		judge.StatusSkip:  "&#x23ED;",
		judge.StatusError: "&#x26A0;",
		judge.Status("x"): "?",
	}
	for status, want := range tests {
		if got := statusIcon(status); got != want {
			t.Fatalf("statusIcon(%q) = %q, want %q", status, got, want)
		}
	}
}
