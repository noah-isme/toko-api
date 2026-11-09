package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/notify"
)

func TestRenderEmailAlertTopics(t *testing.T) {
	cases := []struct {
		topic   string
		subject string
	}{
		{events.TopicAlertBackInStock, "Stok kembali tersedia"},
		{events.TopicAlertPriceDrop, "Harga turun"},
	}
	for _, tc := range cases {
		t.Run(tc.topic, func(t *testing.T) {
			ev := orderEvent(tc.topic, `{"email":"buyer@example.com","message":"Kaos Hitam sudah tersedia kembali."}`)
			to, subject, body, ok := notify.RenderEmail(ev)
			if !ok {
				t.Fatal("expected an alert email to render")
			}
			if to != "buyer@example.com" {
				t.Errorf("recipient = %q", to)
			}
			if subject != tc.subject {
				t.Errorf("subject = %q, want %q", subject, tc.subject)
			}
			if !strings.Contains(body, "Kaos Hitam sudah tersedia kembali.") {
				t.Errorf("body missing message: %q", body)
			}

			enq := &stubEmailEnqueuer{}
			n := notify.EmailNotifier{Queue: enq, Enabled: true}
			if err := n.Notify(context.Background(), ev); err != nil {
				t.Fatalf("notify: %v", err)
			}
			if len(enq.tasks) != 1 || enq.tasks[0].Topic != tc.topic {
				t.Fatalf("expected one queued %s email, got %v", tc.topic, enq.tasks)
			}
		})
	}
}

func TestEmailNotifierAlertTopicToggle(t *testing.T) {
	enq := &stubEmailEnqueuer{}
	n := notify.EmailNotifier{Queue: enq, Enabled: true, TopicToggles: map[string]bool{events.TopicAlertPriceDrop: false}}
	if err := n.Notify(context.Background(), orderEvent(events.TopicAlertPriceDrop, `{"email":"a@b.c"}`)); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(enq.tasks) != 0 {
		t.Fatalf("expected toggle to suppress email, got %v", enq.tasks)
	}
}
