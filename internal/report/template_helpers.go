// Package report — template_helpers.go provides shared helper functions
// for HTML template rendering across different report types.
package report

import (
	"fmt"
	"html/template"
	"reflect"
)

// SharedTemplateFuncs returns the template.FuncMap shared across all HTML reporters.
func SharedTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"fmtDuration": func(ms int64) string {
			return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
		},
		"fmtPercent": func(f float64) string {
			return fmt.Sprintf("%.0f%%", f*100)
		},
		"fmtPercentSigned": func(f float64) string {
			return fmt.Sprintf("%+.0f%%", f*100)
		},
		"passFailClass": func(passed bool) string {
			if passed {
				return "pass"
			}
			return "fail"
		},
		"passFailIcon": func(passed bool) template.HTML {
			if passed {
				return template.HTML("&#x2705;") // ✅
			}
			return template.HTML("&#x274C;") // ❌
		},
		"notNil": func(v any) bool {
			if v == nil {
				return false
			}
			rv := reflect.ValueOf(v)
			switch rv.Kind() {
			case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
				return !rv.IsNil()
			}
			return true
		},
		"derefFloat": func(f *float64) float64 {
			if f == nil {
				return 0
			}
			return *f
		},
	}
}
