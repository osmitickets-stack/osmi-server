package email

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeSESEvent(t *testing.T) {
	attempt := uuid.New()
	raw := []byte(`{"eventType":"Delivery","mail":{"messageId":"ses-message-1","tags":{"osmi_attempt_id":["` + attempt.String() + `"]}}}`)
	event, err := DecodeSESEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != "Delivery" || event.AttemptID != attempt || event.Mail.MessageID != "ses-message-1" {
		t.Fatalf("unexpected parsed event: type=%q attempt=%s", event.EventType, event.AttemptID)
	}
	envelope, err := json.Marshal(map[string]string{"Type": "Notification", "Message": string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSESEvent(envelope); err != nil {
		t.Fatalf("SNS envelope: %v", err)
	}
}

func TestDecodeSESEventRejectsUntrackedAndInvalid(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"eventType":"Bounce","mail":{"messageId":"ses-message-1","tags":{}}}`),
		[]byte(`{"eventType":"Delivery","mail":{"messageId":"ses-message-1","tags":{"osmi_attempt_id":["not-a-uuid"]}}}`),
	} {
		if _, err := DecodeSESEvent(data); !errors.Is(err, ErrUntrackedSESEvent) {
			t.Fatalf("expected untracked event, got %v", err)
		}
	}
	if _, err := DecodeSESEvent([]byte(strings.Repeat("x", 256*1024+1))); err == nil {
		t.Fatal("oversized event was accepted")
	}
	if _, err := DecodeSESEvent([]byte(`{"eventType":"Unknown","mail":{"messageId":"m"}}`)); err == nil {
		t.Fatal("unsupported event was accepted")
	}
}

func TestRecipientHashNormalization(t *testing.T) {
	if recipientHash(" Buyer@Example.com ") != recipientHash("buyer@example.com") {
		t.Fatal("recipient hash must ignore case and outer spaces")
	}
	if recipientHash("buyer@example.com") == "buyer@example.com" {
		t.Fatal("recipient was stored in cleartext")
	}
}

func TestSESEventJobStateAcrossAttempts(t *testing.T) {
	cases := []struct {
		current, incoming string
		isCurrent         bool
		want              bool
	}{
		{"sending", "accepted", true, true},
		{"sending", "bounced", false, false},
		{"accepted", "bounced", false, false},
		{"bounced", "delivered", false, true},
		{"delivered", "bounced", true, false},
		{"rejected", "bounced", true, true},
		{"bounced", "rejected", true, false},
		{"bounced", "delayed", true, false},
		{"delivered", "complained", false, true},
		{"complained", "delivered", true, false},
		{"complained", "bounced", true, false},
		{"retrying", "delivered", false, true},
	}
	for _, tc := range cases {
		if got := shouldAdvanceJob(tc.current, tc.incoming, tc.isCurrent); got != tc.want {
			t.Errorf("status=%s incoming=%s current_attempt=%t: got %t, want %t", tc.current, tc.incoming, tc.isCurrent, got, tc.want)
		}
	}
}

func TestSESEventRankIsMonotonic(t *testing.T) {
	statuses := []string{"accepted", "delayed", "rejected", "bounced", "delivered", "complained"}
	for i := 1; i < len(statuses); i++ {
		if eventRank(statuses[i-1]) >= eventRank(statuses[i]) {
			t.Fatalf("%s must precede %s", statuses[i-1], statuses[i])
		}
	}
}
