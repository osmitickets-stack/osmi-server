package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrUntrackedSESEvent = errors.New("SES event has no tracked OSMI attempt")

type sesRecipient struct {
	EmailAddress string `json:"emailAddress"`
}

type SESEvent struct {
	EventType string `json:"eventType"`
	Mail      struct {
		MessageID   string              `json:"messageId"`
		Destination []string            `json:"destination"`
		Tags        map[string][]string `json:"tags"`
	} `json:"mail"`
	Bounce struct {
		BounceType        string         `json:"bounceType"`
		BouncedRecipients []sesRecipient `json:"bouncedRecipients"`
	} `json:"bounce"`
	Complaint struct {
		ComplainedRecipients []sesRecipient `json:"complainedRecipients"`
	} `json:"complaint"`
	AttemptID uuid.UUID `json:"-"`
}

// DecodeSESEvent accepts raw SNS-to-SQS delivery and the standard SNS envelope.
// It never returns the full payload in an error: SES events contain addresses.
func DecodeSESEvent(data []byte) (*SESEvent, error) {
	if len(data) == 0 || len(data) > 256*1024 {
		return nil, errors.New("SES event payload size is invalid")
	}
	var envelope struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, errors.New("SES event JSON is invalid")
	}
	if envelope.Type == "Notification" {
		data = []byte(envelope.Message)
	}
	var event SESEvent
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&event); err != nil {
		return nil, errors.New("SES event content is invalid")
	}
	if eventStatus(event.EventType) == "" || event.Mail.MessageID == "" || len(event.Mail.MessageID) > 255 {
		return nil, errors.New("SES event type or message ID is invalid")
	}
	tags := event.Mail.Tags["osmi_attempt_id"]
	if len(tags) != 1 {
		return nil, ErrUntrackedSESEvent
	}
	attemptID, err := uuid.Parse(tags[0])
	if err != nil || attemptID == uuid.Nil {
		return nil, ErrUntrackedSESEvent
	}
	event.AttemptID = attemptID
	return &event, nil
}

func eventStatus(eventType string) string {
	switch eventType {
	case "Send":
		return "accepted"
	case "DeliveryDelay":
		return "delayed"
	case "Delivery":
		return "delivered"
	case "Bounce":
		return "bounced"
	case "Complaint":
		return "complained"
	case "Reject", "Rendering Failure":
		return "rejected"
	default:
		return ""
	}
}

func eventRank(status string) int {
	switch status {
	case "accepted":
		return 1
	case "delayed":
		return 2
	case "delivered":
		return 3
	case "bounced", "rejected":
		return 4
	case "complained":
		return 5
	default:
		return 0
	}
}

func shouldAdvanceJob(currentStatus, newStatus string, isCurrentAttempt bool) bool {
	switch newStatus {
	case "complained":
		return currentStatus != "complained"
	case "delivered":
		return currentStatus != "delivered" && currentStatus != "complained"
	case "bounced", "rejected":
		return isCurrentAttempt && currentStatus != "delivered" && currentStatus != "complained" && eventRank(newStatus) > eventRank(currentStatus)
	default:
		return isCurrentAttempt && eventRank(newStatus) > eventRank(currentStatus)
	}
}

func affectedRecipients(event *SESEvent) []string {
	var addresses []string
	switch event.EventType {
	case "Bounce":
		for _, recipient := range event.Bounce.BouncedRecipients {
			addresses = append(addresses, recipient.EmailAddress)
		}
	case "Complaint":
		for _, recipient := range event.Complaint.ComplainedRecipients {
			addresses = append(addresses, recipient.EmailAddress)
		}
	}
	return addresses
}

// RecordSESEvent is idempotent for SQS redelivery and never downgrades a
// delivered or complained message when SES events arrive out of order.
func (o *TicketEmailOutbox) RecordSESEvent(ctx context.Context, event *SESEvent) error {
	if o == nil || o.pool == nil || event == nil {
		return errors.New("SES event handler is not configured")
	}
	newStatus := eventStatus(event.EventType)
	if newStatus == "" || event.AttemptID == uuid.Nil {
		return errors.New("SES event is invalid")
	}
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var jobID int64
	var previousStatus, previousMessageID string
	var orderRecipient string
	err = tx.QueryRow(ctx, `
		SELECT a.job_id, a.status, COALESCE(a.ses_message_id,''), o.customer_email::text
		FROM notifications.ticket_email_attempts a
		JOIN notifications.ticket_email_jobs j ON j.id=a.job_id
		JOIN billing.orders o ON o.id=j.order_id
		WHERE a.id=$1 FOR UPDATE OF a
	`, event.AttemptID.String()).Scan(&jobID, &previousStatus, &previousMessageID, &orderRecipient)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUntrackedSESEvent
	}
	if err != nil {
		return fmt.Errorf("read SES attempt: %w", err)
	}
	if previousMessageID != "" && previousMessageID != event.Mail.MessageID {
		return errors.New("SES message ID changed for attempt")
	}
	if len(event.Mail.Destination) > 0 {
		matched := false
		for _, address := range event.Mail.Destination {
			matched = matched || strings.EqualFold(strings.TrimSpace(address), orderRecipient)
		}
		if !matched {
			return errors.New("SES event recipient does not match order")
		}
	}
	if eventRank(newStatus) >= eventRank(previousStatus) {
		_, err = tx.Exec(ctx, `
			UPDATE notifications.ticket_email_attempts
			SET status=$2, ses_message_id=$3, last_event_at=NOW(),
			    accepted_at=CASE WHEN $2='accepted' THEN COALESCE(accepted_at,NOW()) ELSE accepted_at END
			WHERE id=$1
		`, event.AttemptID.String(), newStatus, event.Mail.MessageID)
	} else if previousMessageID == "" {
		_, err = tx.Exec(ctx, `
			UPDATE notifications.ticket_email_attempts SET ses_message_id=$2 WHERE id=$1
		`, event.AttemptID.String(), event.Mail.MessageID)
	}
	if err != nil {
		return fmt.Errorf("record SES attempt: %w", err)
	}
	var jobStatus string
	var currentAttemptID *string
	err = tx.QueryRow(ctx, `
		SELECT status, current_attempt_id::text
		FROM notifications.ticket_email_jobs WHERE id=$1 FOR UPDATE
	`, jobID).Scan(&jobStatus, &currentAttemptID)
	if err != nil {
		return fmt.Errorf("read SES job: %w", err)
	}
	isCurrent := currentAttemptID != nil && *currentAttemptID == event.AttemptID.String()
	if shouldAdvanceJob(jobStatus, newStatus, isCurrent) {
		_, err = tx.Exec(ctx, `
			UPDATE notifications.ticket_email_jobs
			SET status=$2, next_attempt_at=NULL, lease_until=NULL,
			    last_event_at=NOW(), updated_at=NOW()
			WHERE id=$1
		`, jobID, newStatus)
		if err != nil {
			return fmt.Errorf("record SES job state: %w", err)
		}
	}
	if (event.EventType == "Bounce" && event.Bounce.BounceType == "Permanent") || event.EventType == "Complaint" {
		reason := strings.ToLower(event.EventType)
		// SES may omit the complaining address. A tracked ticket message has
		// exactly one recipient, and mail.destination was checked above.
		matched := false
		for _, address := range affectedRecipients(event) {
			matched = matched || strings.EqualFold(strings.TrimSpace(address), orderRecipient)
		}
		if event.EventType == "Complaint" && len(affectedRecipients(event)) == 0 && len(event.Mail.Destination) == 1 {
			matched = strings.EqualFold(strings.TrimSpace(event.Mail.Destination[0]), orderRecipient)
		}
		if matched && !strings.HasSuffix(strings.ToLower(orderRecipient), "@simulator.amazonses.com") {
			_, err = tx.Exec(ctx, `
				INSERT INTO notifications.email_suppressions(recipient_sha256,reason)
				VALUES ($1,$2)
				ON CONFLICT (recipient_sha256) DO UPDATE
				SET reason=EXCLUDED.reason, updated_at=NOW()
			`, recipientHash(orderRecipient), reason)
			if err != nil {
				return fmt.Errorf("record email suppression: %w", err)
			}
		}
	}
	return tx.Commit(ctx)
}
