package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/email"
)

type testFeedbackQueue struct {
	messages   []types.Message
	receiveErr error
	deleteErr  error
	deletes    int
}

func (q *testFeedbackQueue) ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if q.receiveErr != nil {
		return nil, q.receiveErr
	}
	return &sqs.ReceiveMessageOutput{Messages: q.messages}, nil
}

func (q *testFeedbackQueue) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	q.deletes++
	if q.deleteErr != nil {
		return nil, q.deleteErr
	}
	return &sqs.DeleteMessageOutput{}, nil
}

type testEventRecorder struct {
	err   error
	calls int
}

func (r *testEventRecorder) RecordSESEvent(context.Context, *email.SESEvent) error {
	r.calls++
	return r.err
}

func message(body string) types.Message {
	return types.Message{Body: aws.String(body), ReceiptHandle: aws.String("test-handle")}
}

func trackedEvent() string {
	return fmt.Sprintf(`{"eventType":"Delivery","mail":{"messageId":"test-id","tags":{"osmi_attempt_id":["%s"]}}}`, uuid.NewString())
}

func TestReceiveEventsPreservesUnprocessedMessages(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		recordErr  error
		wantCalls  int
		wantDelete int
	}{
		{name: "malformed JSON", body: "not-json"},
		{name: "unknown event type", body: `{"eventType":"Unexpected","mail":{"messageId":"test-id"}}`},
		{name: "untracked SES event", body: `{"eventType":"Delivery","mail":{"messageId":"test-id","tags":{}}}`},
		{name: "untracked database attempt", body: trackedEvent(), recordErr: email.ErrUntrackedSESEvent, wantCalls: 1},
		{name: "database failure", body: trackedEvent(), recordErr: errors.New("database unavailable"), wantCalls: 1},
		{name: "processed event", body: trackedEvent(), wantCalls: 1, wantDelete: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			queue := &testFeedbackQueue{messages: []types.Message{message(tc.body)}}
			recorder := &testEventRecorder{err: tc.recordErr}
			if err := receiveEvents(context.Background(), queue, "test-queue", recorder); err != nil {
				t.Fatalf("receiveEvents: %v", err)
			}
			if recorder.calls != tc.wantCalls || queue.deletes != tc.wantDelete {
				t.Fatalf("record calls=%d, deletes=%d; want %d, %d", recorder.calls, queue.deletes, tc.wantCalls, tc.wantDelete)
			}
		})
	}
}

func TestReceiveEventsContinuesAfterInvalidMessage(t *testing.T) {
	queue := &testFeedbackQueue{messages: []types.Message{message("not-json"), message(trackedEvent())}}
	recorder := &testEventRecorder{}
	if err := receiveEvents(context.Background(), queue, "test-queue", recorder); err != nil {
		t.Fatalf("receiveEvents: %v", err)
	}
	if recorder.calls != 1 || queue.deletes != 1 {
		t.Fatalf("record calls=%d, deletes=%d; want 1, 1", recorder.calls, queue.deletes)
	}
}

func TestReceiveEventsHandlesQueueErrors(t *testing.T) {
	queue := &testFeedbackQueue{receiveErr: errors.New("temporary receive failure")}
	if err := receiveEvents(context.Background(), queue, "test-queue", &testEventRecorder{}); err == nil {
		t.Fatal("receive error was ignored")
	}
	queue = &testFeedbackQueue{messages: []types.Message{message(trackedEvent())}, deleteErr: errors.New("temporary delete failure")}
	if err := receiveEvents(context.Background(), queue, "test-queue", &testEventRecorder{}); err != nil {
		t.Fatalf("delete error stopped the loop: %v", err)
	}
	if queue.deletes != 1 {
		t.Fatalf("delete attempts=%d; want 1", queue.deletes)
	}
}

func TestSESSendUsesOneSDKAttempt(t *testing.T) {
	client := newSingleAttemptSESClient(aws.Config{Region: "us-east-1"})
	retryer := client.Options().Retryer
	if retryer == nil || retryer.MaxAttempts() != 1 || retryer.IsErrorRetryable(errors.New("timeout")) {
		t.Fatal("SES client may retry one outbox attempt more than once")
	}
}
