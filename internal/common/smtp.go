package common

import (
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// defaultSMTPTimeout bounds the whole conversation with the relay. net/smtp's
// SendMail offers no timeout at all, which is why the dialling below is done by
// hand: a hung relay must not pin a request goroutine indefinitely.
const defaultSMTPTimeout = 10 * time.Second

// SMTPSender delivers mail through an SMTP relay.
//
// Two transport styles are supported: implicit TLS (the relay speaks TLS from
// the first byte, conventionally port 465) and opportunistic STARTTLS on a
// plaintext port (conventionally 587). STARTTLS is used whenever the relay
// advertises it.
type SMTPSender struct {
	Host string
	Port int
	// Username and Password are optional; unauthenticated relays (a local
	// MailHog or a trusted internal smarthost) are supported by leaving them
	// empty.
	Username string
	Password string
	// From is the envelope sender and the From header.
	From string
	// ImplicitTLS dials TLS directly instead of upgrading via STARTTLS.
	ImplicitTLS bool
	// Timeout bounds the full send. Zero means defaultSMTPTimeout.
	Timeout time.Duration
	// AllowInsecureTLS skips certificate verification. Intended for local
	// relays with self-signed certificates; never enable it in production.
	AllowInsecureTLS bool
}

// NewSMTPSender validates the configuration and returns a ready sender.
func NewSMTPSender(s SMTPSender) (*SMTPSender, error) {
	if strings.TrimSpace(s.Host) == "" {
		return nil, errors.New("smtp: host is required")
	}
	if s.Port <= 0 || s.Port > 65535 {
		return nil, fmt.Errorf("smtp: invalid port %d", s.Port)
	}
	if _, err := mail.ParseAddress(s.From); err != nil {
		return nil, fmt.Errorf("smtp: invalid from address %q: %w", s.From, err)
	}
	if s.Timeout <= 0 {
		s.Timeout = defaultSMTPTimeout
	}
	return &s, nil
}

// Send implements EmailSender.
func (s *SMTPSender) Send(to, subject, body string) error {
	if s == nil {
		return errors.New("smtp: sender not configured")
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("smtp: invalid recipient %q: %w", to, err)
	}

	msg := s.buildMessage(recipient.Address, subject, body)
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))

	conn, err := net.DialTimeout("tcp", addr, s.Timeout)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	// One deadline for the entire exchange, refreshed by nothing: a relay that
	// stalls mid-conversation fails rather than hanging.
	if err := conn.SetDeadline(time.Now().Add(s.Timeout)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: set deadline: %w", err)
	}

	tlsConfig := &tls.Config{
		ServerName:         s.Host,
		InsecureSkipVerify: s.AllowInsecureTLS, //nolint:gosec // opt-in for local relays only
	}
	if s.ImplicitTLS {
		conn = tls.Client(conn, tlsConfig)
	}

	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: greet %s: %w", addr, err)
	}
	defer func() { _ = client.Close() }()

	if !s.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("smtp: starttls: %w", err)
			}
		}
	}

	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}

	if err := client.Mail(s.fromAddress()); err != nil {
		return fmt.Errorf("smtp: mail from: %w", err)
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return fmt.Errorf("smtp: rcpt to: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp: close body: %w", err)
	}

	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp: quit: %w", err)
	}
	return nil
}

func (s *SMTPSender) fromAddress() string {
	if parsed, err := mail.ParseAddress(s.From); err == nil {
		return parsed.Address
	}
	return s.From
}

// buildMessage renders RFC 5322 headers plus the body. Line endings are
// normalised to CRLF because bare LF is not valid in SMTP data.
func (s *SMTPSender) buildMessage(to, subject, body string) []byte {
	contentType := `text/plain; charset="UTF-8"`
	if looksLikeHTML(body) {
		contentType = `text/html; charset="UTF-8"`
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", s.From)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: %s\r\n", contentType)
	b.WriteString("\r\n")
	b.WriteString(normaliseNewlines(body))
	return []byte(b.String())
}

// looksLikeHTML decides the content type. The EmailSender contract names the
// body parameter `html`, but every caller today builds plain text; sending that
// as text/html would collapse its newlines. Sniffing keeps both correct.
func looksLikeHTML(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "<") && strings.Contains(trimmed, "</")
}

func normaliseNewlines(body string) string {
	normalised := strings.ReplaceAll(body, "\r\n", "\n")
	return strings.ReplaceAll(normalised, "\n", "\r\n")
}
