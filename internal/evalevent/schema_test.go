package evalevent

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

const schemaBaseURL = "https://raw.githubusercontent.com/alibaba/skill-up/main/schemas/evalevent/v1/"

func TestV1SchemasMatchCoreEventRegistry(t *testing.T) {
	t.Parallel()

	dir := schemaDirectory(t)
	wantRequired := map[Type][]string{
		EventRunStarted:         {"engine", "skill_name", "task_total", "iterations_total"},
		EventRunProgress:        {"phase", "task_total", "completed_tasks", "running_tasks", "passed", "failed", "errored", "skipped", "elapsed_ms"},
		EventIterationStarted:   {"iteration"},
		EventCaseStarted:        {"task_id", "iteration", "case_id", "configuration", "task_index", "task_total", "title"},
		EventCaseCompleted:      {"task_id", "iteration", "case_id", "configuration", "task_index", "task_total", "title", "completed_tasks", "status", "duration_ms"},
		EventIterationCompleted: {"iteration", "invocation_completed_tasks", "passed", "failed", "errored", "skipped", "duration_ms"},
		EventRunFinished:        {"status", "completed_tasks", "passed", "failed", "errored", "skipped", "duration_ms"},
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}
	if len(entries) != len(wantRequired)+1 {
		t.Fatalf("schema file count = %d, want %d", len(entries), len(wantRequired)+1)
	}
	for eventType, required := range wantRequired {
		filename := string(eventType) + "-v1.schema.json"
		path := filepath.Join(dir, filename)
		doc := readSchema(t, path)
		if got, want := schemaStringField(t, doc, "$id"), schemaBaseURL+filename; got != want {
			t.Errorf("%s $id = %q, want %q", path, got, want)
		}
		allOf := schemaArray(t, doc, "allOf")
		if len(allOf) != 2 {
			t.Fatalf("%s allOf count = %d, want 2", path, len(allOf))
		}
		assertEnvelopeReference(t, path, doc, schemaObject(t, allOf[0]))
		eventSchema := schemaObject(t, allOf[1])
		properties := schemaObjectField(t, eventSchema, "properties")
		if got := schemaObjectField(t, properties, "protocol_version")["const"]; got != float64(ProtocolVersion) {
			t.Errorf("%s protocol_version const = %v", path, got)
		}
		if got := schemaObjectField(t, properties, "event_version")["const"]; got != float64(EventVersionV1) {
			t.Errorf("%s event_version const = %v", path, got)
		}
		if got := schemaObjectField(t, properties, "event")["const"]; got != string(eventType) {
			t.Errorf("%s event const = %v, want %q", path, got, eventType)
		}
		payload := schemaObjectField(t, properties, "payload")
		if !schemaBoolField(t, payload, "additionalProperties") {
			t.Errorf("%s payload must allow additive fields", path)
		}
		gotRequired := schemaStrings(t, payload, "required")
		sort.Strings(gotRequired)
		want := append([]string(nil), required...)
		sort.Strings(want)
		if !equalStringLists(gotRequired, want) {
			t.Errorf("%s payload required = %v, want %v", path, gotRequired, want)
		}
	}
}

func TestV1EnvelopeSchemaIsOpenAndRequiresFinalMarkerToBeTrue(t *testing.T) {
	t.Parallel()

	doc := readSchema(t, filepath.Join(schemaDirectory(t), "envelope.schema.json"))
	if got, want := schemaStringField(t, doc, "$id"), schemaBaseURL+"envelope.schema.json"; got != want {
		t.Errorf("envelope $id = %q, want %q", got, want)
	}
	if !schemaBoolField(t, doc, "additionalProperties") {
		t.Fatal("envelope schema must allow unknown optional fields")
	}
	if _, exists := doc["oneOf"]; exists {
		t.Fatal("envelope schema must not use a closed top-level oneOf")
	}
	properties := schemaObjectField(t, doc, "properties")
	if got := schemaObjectField(t, properties, "protocol_version")["const"]; got != float64(ProtocolVersion) {
		t.Errorf("protocol_version const = %v", got)
	}
	if !schemaBoolField(t, schemaObjectField(t, properties, "last_event"), "const") {
		t.Error("last_event const must be true")
	}
	if got := schemaObjectField(t, properties, "payload")["type"]; got != "object" {
		t.Errorf("payload type = %v, want object", got)
	}
}

func assertEnvelopeReference(t *testing.T, path string, doc, reference map[string]any) {
	t.Helper()
	base, err := url.Parse(schemaStringField(t, doc, "$id"))
	if err != nil {
		t.Fatalf("parse %s $id: %v", path, err)
	}
	resolved, err := base.Parse(schemaStringField(t, reference, "$ref"))
	if err != nil {
		t.Fatalf("resolve %s envelope reference: %v", path, err)
	}
	if got, want := resolved.String(), schemaBaseURL+"envelope.schema.json"; got != want {
		t.Errorf("%s envelope reference resolves to %q, want %q", path, got, want)
	}
}

func schemaDirectory(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "schemas", "evalevent", "v1"))
}

func readSchema(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return doc
}

func schemaArray(t *testing.T, object map[string]any, field string) []any {
	t.Helper()
	value, ok := object[field].([]any)
	if !ok {
		t.Fatalf("schema field %q has type %T, want array", field, object[field])
	}
	return value
}

func schemaObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema value has type %T, want object", value)
	}
	return object
}

func schemaObjectField(t *testing.T, object map[string]any, field string) map[string]any {
	t.Helper()
	return schemaObject(t, object[field])
}

func schemaStrings(t *testing.T, object map[string]any, field string) []string {
	t.Helper()
	values := schemaArray(t, object, field)
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("schema field %q item %d has type %T, want string", field, i, value)
		}
		result[i] = text
	}
	return result
}

func schemaBoolField(t *testing.T, object map[string]any, field string) bool {
	t.Helper()
	value, ok := object[field].(bool)
	if !ok {
		t.Fatalf("schema field %q has type %T, want boolean", field, object[field])
	}
	return value
}

func schemaStringField(t *testing.T, object map[string]any, field string) string {
	t.Helper()
	value, ok := object[field].(string)
	if !ok {
		t.Fatalf("schema field %q has type %T, want string", field, object[field])
	}
	return value
}

func equalStringLists(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
