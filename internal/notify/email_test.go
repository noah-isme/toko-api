package notify

import (
	"context"
	"testing"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

type countingMailer struct{ sent []string }

func (m *countingMailer) Send(to, subject, body string) error {
	m.sent = append(m.sent, to)
	return nil
}

func TestEmailNotifierSkipsPaymentLateEvenWithRecipient(t *testing.T) {
	mailer := &countingMailer{}
	n := EmailNotifier{Mail: mailer, Enabled: true, TopicToggles: map[string]bool{events.TopicPaymentExpired: true}}
	payload := []byte(`{"email":"buyer@example.com","orderId":"o-1"}`)
	if err := n.Notify(context.Background(), dbgen.DomainEvent{Topic: events.TopicPaymentLate, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("payment.late must not email the customer, sent to %v", mailer.sent)
	}
	if err := n.Notify(context.Background(), dbgen.DomainEvent{Topic: events.TopicPaymentExpired, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("payment.expired should still email, sent=%v", mailer.sent)
	}
}
