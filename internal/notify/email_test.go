package notify

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

type captureSender struct {
	to, subject, body string
	calls             int
}

func (c *captureSender) Send(to, subject, body string) error {
	c.calls++
	c.to, c.subject, c.body = to, subject, body
	return nil
}

func TestEmailNotifierAlertTopics(t *testing.T) {
	cases := []struct {
		topic   string
		subject string
	}{
		{events.TopicAlertBackInStock, "Stok kembali tersedia"},
		{events.TopicAlertPriceDrop, "Harga turun"},
	}
	for _, tc := range cases {
		t.Run(tc.topic, func(t *testing.T) {
			sender := &captureSender{}
			n := EmailNotifier{Mail: sender, Enabled: true}
			ev := dbgen.DomainEvent{
				Topic:      tc.topic,
				Payload:    []byte(`{"email":"buyer@example.com","message":"Kaos Hitam sudah tersedia kembali."}`),
				OccurredAt: pgtype.Timestamptz{Valid: true},
			}
			if err := n.Notify(context.Background(), ev); err != nil {
				t.Fatalf("notify: %v", err)
			}
			if sender.calls != 1 {
				t.Fatalf("expected one email, got %d", sender.calls)
			}
			if sender.to != "buyer@example.com" {
				t.Errorf("recipient = %q", sender.to)
			}
			if sender.subject != tc.subject {
				t.Errorf("subject = %q, want %q", sender.subject, tc.subject)
			}
			if !strings.Contains(sender.body, "Kaos Hitam sudah tersedia kembali.") {
				t.Errorf("body missing message: %q", sender.body)
			}
		})
	}
}

func TestEmailNotifierAlertTopicToggle(t *testing.T) {
	sender := &captureSender{}
	n := EmailNotifier{Mail: sender, Enabled: true, TopicToggles: map[string]bool{events.TopicAlertPriceDrop: false}}
	ev := dbgen.DomainEvent{Topic: events.TopicAlertPriceDrop, Payload: []byte(`{"email":"a@b.c"}`)}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if sender.calls != 0 {
		t.Fatalf("expected toggle to suppress email, got %d calls", sender.calls)
	}
}
