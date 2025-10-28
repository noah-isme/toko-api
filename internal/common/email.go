package common

// EmailSender defines the contract for sending emails.
type EmailSender interface {
	Send(to, subject, html string) error
}

// InMemoryEmail provides a test-friendly email sender that records messages.
type InMemoryEmail struct {
	Outbox []Email
}

// Email represents a single email message captured by InMemoryEmail.
type Email struct {
	To      string
	Subject string
	HTML    string
}

// Send records the email in memory.
func (m *InMemoryEmail) Send(to, subject, html string) error {
	if m == nil {
		return nil
	}
	m.Outbox = append(m.Outbox, Email{To: to, Subject: subject, HTML: html})
	return nil
}

// NopEmailSender implements EmailSender without performing any action.
type NopEmailSender struct{}

// Send implements EmailSender.
func (NopEmailSender) Send(string, string, string) error { return nil }
