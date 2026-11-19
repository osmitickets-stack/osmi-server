package email

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/google/uuid"
)

type fakeSESAPI struct {
	input     *sesv2.SendEmailInput
	deadline  bool
	err       error
	messageID string
}

func (f *fakeSESAPI) SendEmail(ctx context.Context, input *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	f.input = input
	deadline, ok := ctx.Deadline()
	f.deadline = ok && time.Until(deadline) <= 20*time.Second && time.Until(deadline) > 0
	if f.err != nil {
		return nil, f.err
	}
	return &sesv2.SendEmailOutput{MessageId: aws.String(f.messageID)}, nil
}

func TestSESAPISendsTicketMIMEWithAttempt(t *testing.T) {
	t.Setenv("SES_CONFIGURATION_SET", "osmi-transactional-prod")
	t.Setenv("SMTP_FROM", "boletos@myosmi.com")
	t.Setenv("SMTP_REPLY_TO", "support@example.com")
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")
	api := &fakeSESAPI{messageID: "ses-test-message"}
	sender := NewSESAPIClient(api)
	if err := sender.ValidateConfig(); err != nil {
		t.Fatal(err)
	}
	attempt := uuid.New()
	messageID, err := sender.SendOrderTicketsEmailWithAttempt("buyer@example.com", "Comprador", uuid.NewString(), []TicketEmailItem{{
		TicketCode: "TEST-1", TicketTypeName: "General", EventName: "Evento", EventDate: "mañana", EventLocation: "Sala", QRBase64: "iVBORw0KGgo=",
	}}, attempt)
	if err != nil || messageID != "ses-test-message" {
		t.Fatal(err)
	}
	if !api.deadline || api.input == nil {
		t.Fatal("SES API must be called with a bounded context")
	}
	if aws.ToString(api.input.FromEmailAddress) != "boletos@myosmi.com" ||
		aws.ToString(api.input.ConfigurationSetName) != "osmi-transactional-prod" ||
		len(api.input.Destination.ToAddresses) != 1 || api.input.Destination.ToAddresses[0] != "buyer@example.com" ||
		len(api.input.ReplyToAddresses) != 1 || api.input.ReplyToAddresses[0] != "support@example.com" {
		t.Fatal("SES API destination or configuration does not match")
	}
	if len(api.input.EmailTags) != 1 || aws.ToString(api.input.EmailTags[0].Name) != "osmi_attempt_id" || aws.ToString(api.input.EmailTags[0].Value) != attempt.String() {
		t.Fatal("outbox attempt tag missing")
	}
	raw := string(api.input.Content.Raw.Data)
	for _, part := range []string{"text/plain", "text/html", "Content-ID: <ticket-qr-1>", "TEST-1"} {
		if !strings.Contains(raw, part) {
			t.Fatalf("raw MIME is missing %q", part)
		}
	}
	if strings.Contains(raw, "X-SES-MESSAGE-TAGS") || strings.Contains(raw, "X-SES-CONFIGURATION-SET") {
		t.Fatal("SES API tags must be passed as structured request fields")
	}
}

func TestSESAPIFailureDoesNotExposeRecipient(t *testing.T) {
	t.Setenv("SES_CONFIGURATION_SET", "osmi-transactional-prod")
	t.Setenv("SMTP_FROM", "boletos@myosmi.com")
	api := &fakeSESAPI{err: errors.New("buyer@example.com private provider error")}
	sender := NewSESAPIClient(api)
	items := []TicketEmailItem{{TicketCode: "TEST-1", QRBase64: "iVBORw0KGgo="}}
	_, err := sender.SendOrderTicketsEmailWithAttempt("buyer@example.com", "", uuid.NewString(), items, uuid.New())
	if err == nil || strings.Contains(err.Error(), "buyer@example.com") {
		t.Fatal("SES API error must be redacted")
	}
	api.err = nil
	_, err = sender.SendOrderTicketsEmailWithAttempt("buyer@example.com", "", uuid.NewString(), items, uuid.New())
	if err == nil || !strings.Contains(err.Error(), "no message ID") {
		t.Fatal("missing SES message ID must remain retryable")
	}
}
