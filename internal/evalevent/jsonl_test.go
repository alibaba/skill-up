package evalevent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestJSONLSinkHandlesShortWritesAndTerminatesRecord(t *testing.T) {
	t.Parallel()

	writer := &chunkWriteCloser{maxWrite: 7}
	sink := NewJSONLSink(writer)
	event := testEvent(RunStartedPayload{Engine: "test", SkillName: "skill", TaskTotal: 1, IterationsTotal: 1})
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !bytes.HasSuffix(writer.Bytes(), []byte{'\n'}) {
		t.Fatalf("record is not newline terminated: %q", writer.Bytes())
	}
	if bytes.Count(writer.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("record contains unexpected newlines: %q", writer.Bytes())
	}
	if writer.closeCount != 1 {
		t.Fatalf("writer close count = %d, want 1", writer.closeCount)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}
	if writer.closeCount != 1 {
		t.Fatalf("writer close count after repeated close = %d, want 1", writer.closeCount)
	}
}

func TestJSONLSinkReportsPartialAndZeroWrites(t *testing.T) {
	t.Parallel()

	writeFailure := errors.New("write failed")
	tests := []struct {
		name   string
		writer io.WriteCloser
		want   string
	}{
		{name: "partial error", writer: &failingWriteCloser{n: 3, err: writeFailure}, want: "write failed"},
		{name: "zero write", writer: &failingWriteCloser{}, want: "short write"},
		{name: "nil writer", writer: nil, want: "must not be nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := NewJSONLSink(tt.writer)
			err := sink.Publish(context.Background(), testEvent(validProgressPayload()))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Publish() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestJSONLSinkHonorsCanceledContextBeforeWriting(t *testing.T) {
	t.Parallel()

	writer := &chunkWriteCloser{maxWrite: 10}
	sink := NewJSONLSink(writer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.Publish(ctx, testEvent(validProgressPayload())); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() error = %v, want context.Canceled", err)
	}
	if writer.Len() != 0 {
		t.Fatalf("writer received %d bytes after cancellation", writer.Len())
	}
}

func TestJSONLSinkRejectsOversizedRecord(t *testing.T) {
	t.Parallel()

	writer := &chunkWriteCloser{maxWrite: maxJSONEventSize}
	sink := NewJSONLSink(writer)
	payload := CaseStartedPayload{TaskFields: TaskFields{
		TaskID: "task-1", Iteration: 1, CaseID: "case-1", Configuration: ConfigurationWithSkill,
		TaskIndex: 1, TaskTotal: 1, Title: strings.Repeat("x", maxJSONEventSize),
	}}
	if err := sink.Publish(context.Background(), testEvent(payload)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Publish() error = %v, want size-limit error", err)
	}
	if writer.Len() != 0 {
		t.Fatalf("writer received %d bytes for oversized record", writer.Len())
	}
}

//nolint:cyclop // The test verifies create, write, truncate, and mode preservation as one file lifecycle.
func TestJSONLFileSinkCreatesTruncatesAndPreservesMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	sink, err := NewJSONLFileSink(path)
	if err != nil {
		t.Fatalf("NewJSONLFileSink() error = %v", err)
	}
	if err := sink.Publish(context.Background(), testEvent(validProgressPayload())); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("new file mode = %o, want no group/other permissions", info.Mode().Perm())
	}

	if err := os.Chmod(path, 0o640); err != nil { //nolint:gosec // The test verifies preservation of an existing mode.
		t.Fatalf("Chmod() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("stale trailing data"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sink, err = NewJSONLFileSink(path)
	if err != nil {
		t.Fatalf("NewJSONLFileSink(existing) error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close(existing) error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(contents) != 0 {
		t.Fatalf("existing file was not truncated: %q", contents)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(existing) error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Errorf("existing file mode = %o, want 640", info.Mode().Perm())
	}
}

func TestJSONLFileSinkRequiresExistingParent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing", "events.jsonl")
	if _, err := NewJSONLFileSink(path); err == nil {
		t.Fatal("NewJSONLFileSink() succeeded with missing parent")
	}
}

func testEvent(payload Payload) Event {
	return Event{
		ProtocolVersion: 1,
		EventVersion:    payload.eventVersion(),
		SequenceNumber:  1,
		InvocationID:    "018f8f20-7a7d-7d90-a192-4f5ec8f07a2a",
		Time:            time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC),
		Type:            payload.eventType(),
		Payload:         payload,
	}
}

type chunkWriteCloser struct {
	bytes.Buffer

	maxWrite   int
	closeCount int
}

func (w *chunkWriteCloser) Write(p []byte) (int, error) {
	limit := min(len(p), w.maxWrite)
	return w.Buffer.Write(p[:limit])
}

func (w *chunkWriteCloser) Close() error {
	w.closeCount++
	return nil
}

type failingWriteCloser struct {
	n   int
	err error
}

func (w *failingWriteCloser) Write(p []byte) (int, error) {
	return min(w.n, len(p)), w.err
}

func (w *failingWriteCloser) Close() error { return nil }
