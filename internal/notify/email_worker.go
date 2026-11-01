package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// EventLoader loads a domain event by id. Satisfied by *dbgen.Queries.
type EventLoader interface {
	GetDomainEvent(ctx context.Context, id pgtype.UUID) (dbgen.DomainEvent, error)
}

// EmailWorker handles email-delivery tasks in cmd/worker: it loads the event,
// renders the email and sends it. A returned error makes the queue retry with
// backoff and, after the last attempt, park the task in queue_dlq.
//
// Errors returned to the queue are redacted (failure class, SMTP code and relay
// host only): the queue stores them in queue_dlq.last_error, which every
// tenant's admin can read, and relay replies often echo the recipient address.
type EmailWorker struct {
	Events       EventLoader
	Mail         common.EmailSender
	Enabled      bool
	TopicToggles map[string]bool
	// RelayHost names the SMTP relay in redacted errors (SMTP_HOST).
	RelayHost string
	Logger    *zerolog.Logger
}

// Handle processes one email-delivery task payload.
func (w *EmailWorker) Handle(ctx context.Context, payload []byte) error {
	if w == nil || w.Events == nil || w.Mail == nil {
		return errors.New("email worker: not configured")
	}
	var task EmailTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return fmt.Errorf("email worker: invalid payload: %w", err)
	}
	parsed, err := uuid.Parse(strings.TrimSpace(task.EventID))
	if err != nil {
		return fmt.Errorf("email worker: invalid event id: %w", err)
	}
	logger := w.logger().With().Str("event_id", parsed.String()).Str("topic", task.Topic).Logger()

	event, err := w.Events.GetDomainEvent(ctx, pgtype.UUID{Bytes: parsed, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			logger.Warn().Msg("email task dropped: event not found")
			return nil
		}
		return fmt.Errorf("email worker: load event: %w", err)
	}
	// The worker's configuration is authoritative at send time.
	if !w.Enabled || !topicEnabled(w.TopicToggles, event.Topic) {
		logger.Debug().Msg("email task skipped: topic disabled")
		return nil
	}
	to, subject, body, ok := RenderEmail(event)
	if !ok {
		logger.Debug().Msg("email task skipped: no recipient")
		return nil
	}
	sendErr := w.Mail.Send(to, subject, body)
	if sendErr == nil {
		return nil
	}

	failure := classifySendError(sendErr)
	entry := logger.Warn().Str("class", failure.class).Str("relay", w.RelayHost)
	if failure.code != 0 {
		entry = entry.Int("smtp_code", failure.code)
	}
	if failure.safeToLog {
		entry = entry.Err(sendErr)
	}
	if failure.permanent {
		entry.Msg("email dropped: permanent delivery failure")
		return nil
	}
	entry.Msg("email send failed; the queue will retry")
	return failure.redacted(w.RelayHost)
}

// sendFailure is the PII-free description of a failed send.
type sendFailure struct {
	class     string
	code      int
	timeout   bool
	permanent bool
	// safeToLog is true when the raw error cannot contain the recipient (the
	// conversation failed before RCPT TO).
	safeToLog bool
}

func (f sendFailure) redacted(host string) error {
	msg := "email-delivery: " + f.class + " failed"
	if f.code != 0 {
		msg += fmt.Sprintf(" (smtp %d)", f.code)
	}
	if f.timeout {
		msg += " (timeout)"
	}
	if host != "" {
		msg += " via " + host
	}
	return errors.New(msg)
}

// recipientFreeStages are SMTP stages that happen before the recipient is
// sent, so their errors never contain the address.
var recipientFreeStages = map[string]bool{
	"dial": true, "set deadline": true, "greet": true, "starttls": true, "auth": true, "mail from": true,
}

func classifySendError(err error) sendFailure {
	f := sendFailure{class: "send"}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		f.timeout = true
	}
	if errors.Is(err, common.ErrInvalidRecipient) {
		f.class = "invalid recipient"
		f.permanent = true
		return f
	}
	var smtpErr *common.SendError
	if errors.As(err, &smtpErr) {
		f.class = "smtp " + smtpErr.Stage
		f.code = smtpErr.Code()
		f.safeToLog = recipientFreeStages[smtpErr.Stage]
		// A 5xx reply to RCPT TO means this mailbox will never accept the
		// message; retrying only fills the DLQ.
		f.permanent = smtpErr.Stage == "rcpt to" && smtpErr.Permanent()
	}
	return f
}

func (w *EmailWorker) logger() *zerolog.Logger {
	if w.Logger == nil {
		nop := zerolog.Nop()
		return &nop
	}
	return w.Logger
}
