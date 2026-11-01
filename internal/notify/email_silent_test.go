package notify_test

import (
	"context"
	"testing"

	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/notify"
)

func TestEmailNotifierSkipsPaymentLateEvenWithRecipient(t *testing.T) {
	enq := &stubEmailEnqueuer{}
	n := notify.EmailNotifier{Queue: enq, Enabled: true, TopicToggles: map[string]bool{events.TopicPaymentExpired: true}}
	payload := `{"email":"buyer@example.com","orderId":"o-1"}`
	if err := n.Notify(context.Background(), orderEvent(events.TopicPaymentLate, payload)); err != nil {
		t.Fatal(err)
	}
	if len(enq.tasks) != 0 {
		t.Fatalf("payment.late must not email the customer, queued %v", enq.tasks)
	}
	if err := n.Notify(context.Background(), orderEvent(events.TopicPaymentExpired, payload)); err != nil {
		t.Fatal(err)
	}
	if len(enq.tasks) != 1 {
		t.Fatalf("payment.expired should still email, queued %v", enq.tasks)
	}
}

func TestEmailNotifierSkipsProofUnreviewed(t *testing.T) {
	enq := &stubEmailEnqueuer{}
	n := notify.EmailNotifier{Queue: enq, Enabled: true}
	payload := `{"email":"buyer@example.com","orderId":"o-1"}`
	if err := n.Notify(context.Background(), orderEvent(events.TopicPaymentProofUnreviewed, payload)); err != nil {
		t.Fatal(err)
	}
	if len(enq.tasks) != 0 {
		t.Fatalf("payment.proof_unreviewed must not email the customer, queued %v", enq.tasks)
	}
}
