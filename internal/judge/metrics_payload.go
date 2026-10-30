package judge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Structured named-metrics result payload for the script judge (#317, slice 1).
// The runner assigns one result path per invocation; stdout is never a score
// channel and there is no fallback parsing.

// MetricsSchemaVersion is the only payload version accepted.
const MetricsSchemaVersion = 1

// MaxMetricsPayloadBytes bounds a single collected result file.
const MaxMetricsPayloadBytes = 1 << 20

var metricsEntryFields = map[string]bool{
	"metric_id":    true,
	"availability": true,
	"value":        true,
	"reason":       true,
	"evidence":     true,
}

// ParseMetricsPayload validates a collected result file. A whole-envelope defect
// returns a PayloadProblem so every planned slot becomes ERROR; a defect confined
// to one entry is returned as that entry so only its own slot is poisoned.
func ParseMetricsPayload(raw []byte, invocationID string) ([]EmittedMetric, PayloadProblem) {
	if len(raw) > MaxMetricsPayloadBytes {
		return nil, ProblemSizeLimit
	}
	if problem := checkTopLevelKeys(raw); problem != ProblemNone {
		return nil, problem
	}
	var envelope struct {
		SchemaVersion int                          `json:"schema_version"`
		InvocationID  string                       `json:"invocation_id"`
		Metrics       []map[string]json.RawMessage `json:"metrics"`
	}
	reader := bytes.NewReader(raw)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return nil, ProblemTopLevelShape
		}
		if strings.Contains(err.Error(), "unknown field") {
			return nil, ProblemUnknownTopField
		}
		return nil, ProblemUnparseable
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, ProblemTopLevelShape // trailing content after one value
	}
	if envelope.SchemaVersion != MetricsSchemaVersion {
		return nil, ProblemSchemaVersion
	}
	if invocationID != "" && envelope.InvocationID != invocationID {
		return nil, ProblemUnboundResult
	}
	if envelope.Metrics == nil {
		return nil, ProblemTopLevelShape
	}
	out := make([]EmittedMetric, 0, len(envelope.Metrics))
	for _, entry := range envelope.Metrics {
		metric, problem := decodeMetricsEntry(entry)
		if problem != ProblemNone {
			return nil, problem
		}
		out = append(out, metric)
	}
	return out, ProblemNone
}

// decodeMetricsEntry reads one entry. Ignoring the grammar or declaring a field
// of the wrong kind is a payload defect; a non-numeric or absent value stays a
// slot-level problem so only that slot becomes ERROR.
func decodeMetricsEntry(entry map[string]json.RawMessage) (EmittedMetric, PayloadProblem) {
	for field := range entry {
		if !metricsEntryFields[field] {
			return EmittedMetric{}, ProblemUnknownTopField
		}
	}
	var metric EmittedMetric
	for field, target := range map[string]any{
		"metric_id":    &metric.MetricID,
		"availability": &metric.Availability,
		"reason":       &metric.Reason,
		"evidence":     &metric.Evidence,
	} {
		raw, ok := entry[field]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return EmittedMetric{}, ProblemTopLevelShape
		}
	}
	if raw, ok := entry["value"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			// Non-numeric value: claim AVAILABLE with no value so the slot is
			// poisoned on its own instead of failing the whole payload.
			metric.Availability = string(Available)
			return metric, ProblemNone
		}
		metric.Value = &value
	}
	return metric, ProblemNone
}

// checkTopLevelKeys reports duplicate JSON keys at any nesting level and a
// non-object top level. A duplicate makes binding ambiguous, so nothing in the
// payload can be trusted.
func checkTopLevelKeys(raw []byte) PayloadProblem {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return ProblemUnparseable
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return ProblemTopLevelShape
	}
	return scanObject(decoder)
}

func scanObject(decoder *json.Decoder) PayloadProblem {
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ProblemUnparseable
		}
		key, ok := keyToken.(string)
		if !ok {
			return ProblemTopLevelShape
		}
		if seen[key] {
			return ProblemDuplicateJSONKey
		}
		seen[key] = true
		valueToken, err := decoder.Token()
		if err != nil {
			return ProblemUnparseable
		}
		if problem := scanValue(decoder, valueToken); problem != ProblemNone {
			return problem
		}
	}
	if _, err := decoder.Token(); err != nil { // consume the closing brace
		return ProblemUnparseable
	}
	return ProblemNone
}

func scanValue(decoder *json.Decoder, token json.Token) PayloadProblem {
	delim, isDelim := token.(json.Delim)
	switch {
	case !isDelim:
		return ProblemNone
	case delim == '{':
		return scanObject(decoder)
	case delim == '[':
		for decoder.More() {
			item, err := decoder.Token()
			if err != nil {
				return ProblemUnparseable
			}
			if problem := scanValue(decoder, item); problem != ProblemNone {
				return problem
			}
		}
		if _, err := decoder.Token(); err != nil { // consume the closing bracket
			return ProblemUnparseable
		}
		return ProblemNone
	default:
		return ProblemTopLevelShape
	}
}

// MetricsPayloadTemplate documents the exact grammar a script must satisfy.
func MetricsPayloadTemplate(invocationID string) string {
	return fmt.Sprintf(`{"schema_version":%d,"invocation_id":%q,"metrics":[{"metric_id":"...","availability":"AVAILABLE","value":0.0,"evidence":["..."]}]}`,
		MetricsSchemaVersion, invocationID)
}
