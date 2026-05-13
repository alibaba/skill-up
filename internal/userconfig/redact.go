package userconfig

import (
	"reflect"
	"regexp"
)

const (
	secretTag  = "secret"
	redactMask = "***"
)

var envRefPattern = regexp.MustCompile(`^\$\{[A-Z0-9_]+\}$`)

// Redact returns a copy of c with every string field tagged `secret:"true"`
// masked. Values that match the ${ENV_VAR} pattern are preserved verbatim so
// that env references remain visible.
//
// Currently no Config field carries the secret tag; this is forward-compatible
// for future fields and is exercised by tests with a small fixture struct.
func Redact(c Config) Config {
	out := c
	redactValue(reflect.ValueOf(&out).Elem())
	return out
}

func redactValue(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		redactStruct(v)
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			redactValue(v.Elem())
		}
	}
}

func redactStruct(v reflect.Value) {
	t := v.Type()
	for i := range v.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		fv := v.Field(i)
		if field.Tag.Get(secretTag) == "true" && fv.Kind() == reflect.String {
			s := fv.String()
			if s != "" && !envRefPattern.MatchString(s) {
				fv.SetString(redactMask)
			}
			continue
		}
		if fv.Kind() == reflect.Struct {
			redactStruct(fv)
		}
	}
}
