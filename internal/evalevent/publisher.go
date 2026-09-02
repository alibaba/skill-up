package evalevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxAttributesSize = 16 * 1024
	maxAttributeCount = 32
	maxAttributeKey   = 128
	maxAttributeValue = 1024
)

var attributeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`)

type attributeValidation struct {
	rejectReserved bool
}

type publicationOptions struct {
	lastEvent bool
}

// EventSink transports fully enveloped events in publication order.
type EventSink interface {
	Publish(ctx context.Context, event Event) error
	Close() error
}

// PublisherConfig configures an invocation-scoped Publisher.
type PublisherConfig struct {
	Sink         EventSink
	Attributes   map[string]string
	InvocationID string
	Now          func() time.Time
}

// Publisher assigns event identity and serializes concurrent publication.
type Publisher struct {
	mu sync.Mutex

	sink         EventSink
	attributes   map[string]string
	invocationID string
	now          func() time.Time
	nextSequence uint64

	finalized bool
	closed    bool
	err       error
}

// NewPublisher creates one invocation-scoped publisher and takes ownership of its Sink.
func NewPublisher(cfg PublisherConfig) (*Publisher, error) {
	if isNilInterface(cfg.Sink) {
		return nil, errors.New("event sink must not be nil")
	}
	attributes, err := copyAndValidateAttributes(cfg.Attributes, attributeValidation{})
	if err != nil {
		return nil, err
	}
	invocationID := cfg.InvocationID
	if invocationID == "" {
		invocationID = uuid.NewString()
	} else if _, err := uuid.Parse(invocationID); err != nil {
		return nil, fmt.Errorf("invalid invocation ID: %w", err)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Publisher{
		sink:         cfg.Sink,
		attributes:   attributes,
		invocationID: invocationID,
		now:          now,
		nextSequence: 1,
	}, nil
}

// ValidateUserAttributes validates invocation attributes accepted from a CLI caller.
func ValidateUserAttributes(attributes map[string]string) error {
	_, err := copyAndValidateAttributes(attributes, attributeValidation{rejectReserved: true})
	return err
}

// InvocationID returns the stable UUID assigned to this invocation.
func (p *Publisher) InvocationID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.invocationID
}

// Publish validates and writes one non-final event.
func (p *Publisher) Publish(ctx context.Context, payload Payload) error {
	return p.publishEvent(ctx, payload, publicationOptions{})
}

// PublishLast writes the only event carrying last_event=true and closes publication.
func (p *Publisher) PublishLast(ctx context.Context, payload Payload) error {
	return p.publishEvent(ctx, payload, publicationOptions{lastEvent: true})
}

func (p *Publisher) publishEvent(ctx context.Context, payload Payload, opts publicationOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("publisher is closed")
	}
	if p.finalized {
		return errors.New("publisher already published its last event")
	}
	if p.err != nil {
		return p.err
	}
	if isNilInterface(payload) {
		return errors.New("event payload must not be nil")
	}
	payload = payload.snapshot()
	if err := payload.validate(); err != nil {
		return fmt.Errorf("invalid %s payload: %w", payload.eventType(), err)
	}
	if p.nextSequence > MaxSafeInteger {
		return fmt.Errorf("sequence number exceeds %d", MaxSafeInteger)
	}

	event := Event{
		ProtocolVersion: ProtocolVersion,
		EventVersion:    payload.eventVersion(),
		SequenceNumber:  p.nextSequence,
		InvocationID:    p.invocationID,
		Time:            p.now().UTC(),
		Type:            payload.eventType(),
		LastEvent:       opts.lastEvent,
		Attributes:      copyAttributes(p.attributes),
		Payload:         payload,
	}
	if err := p.sink.Publish(ctx, event); err != nil {
		p.err = fmt.Errorf("publish %s event: %w", event.Type, err)
		return p.err
	}
	p.nextSequence++
	if opts.lastEvent {
		p.finalized = true
	}
	return nil
}

// Err returns the sticky publication or close error without clearing it.
func (p *Publisher) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Close idempotently closes the owned Sink and returns all recorded errors.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.err
	}
	p.closed = true
	if err := p.sink.Close(); err != nil {
		p.err = errors.Join(p.err, fmt.Errorf("close event sink: %w", err))
	}
	return p.err
}

func copyAndValidateAttributes(attributes map[string]string, validation attributeValidation) (map[string]string, error) {
	if len(attributes) > maxAttributeCount {
		return nil, fmt.Errorf("attributes exceed the limit of %d", maxAttributeCount)
	}
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		if !utf8.ValidString(key) || len(key) > maxAttributeKey || !attributeKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid attribute key %q", key)
		}
		if validation.rejectReserved && strings.HasPrefix(key, "skill-up.") {
			return nil, fmt.Errorf("attribute key %q uses the reserved skill-up namespace", key)
		}
		if value == "" {
			return nil, fmt.Errorf("attribute %q has an empty value", key)
		}
		if !utf8.ValidString(value) || len(value) > maxAttributeValue {
			return nil, fmt.Errorf("attribute %q value must be valid UTF-8 and at most %d bytes", key, maxAttributeValue)
		}
		cloned[key] = value
	}
	encoded, err := json.Marshal(cloned)
	if err != nil {
		return nil, fmt.Errorf("encode attributes: %w", err)
	}
	if len(encoded) > maxAttributesSize {
		return nil, fmt.Errorf("serialized attributes exceed %d bytes", maxAttributesSize)
	}
	return cloned, nil
}

func copyAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(attributes))
	maps.Copy(cloned, attributes)
	return cloned
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
