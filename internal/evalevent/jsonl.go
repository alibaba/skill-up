package evalevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const maxJSONEventSize = 1 << 20

var errJSONLSinkClosed = errors.New("JSONL event sink is closed")

// JSONLSink writes complete, newline-terminated event records synchronously.
type JSONLSink struct {
	mu sync.Mutex

	writer   io.WriteCloser
	closed   bool
	closeErr error
}

// NewJSONLFileSink creates or truncates path and requests mode 0600 for a new file.
func NewJSONLFileSink(path string) (*JSONLSink, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open JSONL event log: %w", err)
	}
	return NewJSONLSink(file), nil
}

// NewJSONLSink wraps an unbuffered writer. The sink takes ownership of writer.
func NewJSONLSink(writer io.WriteCloser) *JSONLSink {
	return &JSONLSink{writer: writer}
}

// Publish writes one UTF-8 JSON object followed by a newline.
func (s *JSONLSink) Publish(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if len(record) > maxJSONEventSize {
		return fmt.Errorf("serialized event is %d bytes; limit is %d", len(record), maxJSONEventSize)
	}
	record = append(record, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errJSONLSinkClosed
	}
	if s.writer == nil {
		return errors.New("JSONL writer must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for len(record) > 0 {
		written, writeErr := s.writer.Write(record)
		if written < 0 || written > len(record) {
			return fmt.Errorf("write JSONL event: invalid write count %d", written)
		}
		record = record[written:]
		if writeErr != nil {
			return fmt.Errorf("write JSONL event: %w", writeErr)
		}
		if written == 0 {
			return fmt.Errorf("write JSONL event: %w", io.ErrShortWrite)
		}
	}
	return nil
}

// Close idempotently closes the owned writer.
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	s.closed = true
	if s.writer == nil {
		s.closeErr = errors.New("JSONL writer must not be nil")
		return s.closeErr
	}
	s.closeErr = s.writer.Close()
	return s.closeErr
}
