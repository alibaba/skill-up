package evalevent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPublisherSerializesConcurrentEventsAndPublishesOneLastMarker(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	fixedTime := time.Date(2026, 8, 19, 10, 0, 0, 0, time.FixedZone("test", 8*60*60))
	attributes := map[string]string{"com.example.build_id": "build-1"}
	publisher, err := NewPublisher(PublisherConfig{
		Sink:         sink,
		Attributes:   attributes,
		InvocationID: "018f8f20-7a7d-7d90-a192-4f5ec8f07a2a",
		Now:          func() time.Time { return fixedTime },
	})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	attributes["com.example.build_id"] = "mutated"

	const eventCount = 40
	var wg sync.WaitGroup
	for range eventCount {
		wg.Go(func() {
			if err := publisher.Publish(context.Background(), validProgressPayload()); err != nil {
				t.Errorf("Publish() error = %v", err)
			}
		})
	}
	wg.Wait()
	if err := publisher.PublishLast(context.Background(), RunFinishedPayload{Status: RunStatusCompleted}); err != nil {
		t.Fatalf("PublishLast() error = %v", err)
	}
	if err := publisher.Publish(context.Background(), validProgressPayload()); err == nil {
		t.Fatal("Publish() after last event succeeded")
	}
	if err := publisher.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	events := sink.snapshot()
	if len(events) != eventCount+1 {
		t.Fatalf("event count = %d, want %d", len(events), eventCount+1)
	}
	for i, event := range events {
		if event.SequenceNumber != uint64(i+1) {
			t.Errorf("event[%d].SequenceNumber = %d, want %d", i, event.SequenceNumber, i+1)
		}
		if event.InvocationID != publisher.InvocationID() {
			t.Errorf("event[%d].InvocationID = %q", i, event.InvocationID)
		}
		if event.Time.Location() != time.UTC {
			t.Errorf("event[%d].Time location = %v, want UTC", i, event.Time.Location())
		}
		if event.Attributes["com.example.build_id"] != "build-1" {
			t.Errorf("event[%d] attributes mutated: %v", i, event.Attributes)
		}
		if event.LastEvent != (i == len(events)-1) {
			t.Errorf("event[%d].LastEvent = %t", i, event.LastEvent)
		}
	}
	if sink.closes() != 1 {
		t.Errorf("sink close count = %d, want 1", sink.closes())
	}
}

func TestPublisherSnapshotsPointerPayload(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	publisher, err := NewPublisher(PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	rate := 0.25
	payload := &CaseCompletedPayload{
		TaskFields: TaskFields{
			TaskID: "task-1", Iteration: 1, CaseID: "case-1", Configuration: ConfigurationWithSkill,
			TaskIndex: 1, TaskTotal: 1,
		},
		CompletedTasks: 1,
		Status:         CaseStatusFail,
		PassRate:       &rate,
	}
	if err := publisher.Publish(context.Background(), payload); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	rate = 1
	payload.Status = CaseStatusPass

	stored, ok := sink.snapshot()[0].Payload.(CaseCompletedPayload)
	if !ok {
		t.Fatalf("stored payload type = %T", sink.snapshot()[0].Payload)
	}
	if stored.Status != CaseStatusFail || stored.PassRate == nil || *stored.PassRate != 0.25 {
		t.Fatalf("stored payload was mutated: %+v", stored)
	}
}

func TestPublisherCanonicalizesConfiguredInvocationID(t *testing.T) {
	t.Parallel()

	publisher, err := NewPublisher(PublisherConfig{
		Sink:         newRecordingSink(),
		InvocationID: "018f8f207a7d7d90a1924f5ec8f07a2a",
	})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	if got, want := publisher.InvocationID(), "018f8f20-7a7d-7d90-a192-4f5ec8f07a2a"; got != want {
		t.Fatalf("InvocationID() = %q, want %q", got, want)
	}
}

func TestPublisherStickyFailureAndIdempotentClose(t *testing.T) {
	t.Parallel()

	publishFailure := errors.New("disk full")
	closeFailure := errors.New("close failed")
	sink := newRecordingSink()
	sink.failAt = 2
	sink.publishErr = publishFailure
	sink.closeErr = closeFailure
	publisher, err := NewPublisher(PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	if err := publisher.Publish(context.Background(), validProgressPayload()); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	secondErr := publisher.Publish(context.Background(), validProgressPayload())
	if !errors.Is(secondErr, publishFailure) {
		t.Fatalf("second Publish() error = %v, want disk failure", secondErr)
	}
	thirdErr := publisher.Publish(context.Background(), validProgressPayload())
	if !errors.Is(thirdErr, publishFailure) || thirdErr.Error() != secondErr.Error() {
		t.Fatalf("sticky Publish() error changed: %v vs %v", thirdErr, secondErr)
	}
	if len(sink.snapshot()) != 1 {
		t.Fatalf("sink received %d events after sticky failure, want 1", len(sink.snapshot()))
	}
	closeErr := publisher.Close()
	if !errors.Is(closeErr, publishFailure) || !errors.Is(closeErr, closeFailure) {
		t.Fatalf("Close() error = %v, want joined publish and close failures", closeErr)
	}
	if repeated := publisher.Close(); !errors.Is(repeated, publishFailure) || !errors.Is(repeated, closeFailure) {
		t.Fatalf("repeated Close() error = %v", repeated)
	}
	if publisher.Err().Error() != closeErr.Error() {
		t.Fatalf("Err() = %v, want Close() result %v", publisher.Err(), closeErr)
	}
	if sink.closes() != 1 {
		t.Fatalf("sink close count = %d, want 1", sink.closes())
	}
}

func TestPublisherRejectsInvalidPayloadWithoutConsumingSequence(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	publisher, err := NewPublisher(PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	if err := publisher.Publish(context.Background(), RunStartedPayload{}); err == nil {
		t.Fatal("invalid payload was accepted")
	}
	var nilPayload *RunStartedPayload
	if err := publisher.Publish(context.Background(), nilPayload); err == nil {
		t.Fatal("typed nil payload was accepted")
	}
	if err := publisher.Publish(context.Background(), validProgressPayload()); err != nil {
		t.Fatalf("valid Publish() error = %v", err)
	}
	if got := sink.snapshot()[0].SequenceNumber; got != 1 {
		t.Fatalf("first valid sequence = %d, want 1", got)
	}
}

func TestValidateUserAttributes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		attributes map[string]string
		wantErr    bool
	}{
		{name: "valid", attributes: map[string]string{"com.alibaba.aone.eval_task_id": "123"}},
		{name: "not namespaced", attributes: map[string]string{"build_id": "123"}, wantErr: true},
		{name: "reserved", attributes: map[string]string{"skill-up.trace_id": "123"}, wantErr: true},
		{name: "empty value", attributes: map[string]string{"com.example.key": ""}, wantErr: true},
		{name: "oversized value", attributes: map[string]string{"com.example.key": fmt.Sprintf("%01025d", 1)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateUserAttributes(tt.attributes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateUserAttributes() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func validProgressPayload() RunProgressPayload {
	return RunProgressPayload{Phase: RunPhaseExecuting, TaskTotal: 1}
}
