package common

import (
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// defaultSMTPTimeout bounds the whole conversation with the relay. net/smtp's
// SendMail offers no timeout at all, which is why the dialling below is done by
// hand: a hung relay must not pin a request goroutine indefinitely.
const defaultSMTPTimeout = 10 * time.Second

// ErrInvalidRecipient is returned (wrapped) when the recipient address does not
// parse. The address itself is deliberately not part of the error text, since
// errors end up in logs and job dead-letter entries.
var ErrInvalidRecipient = errors.New("smtp: invalid recipient address")

// SendError reports which step of the SMTP conversation failed. Err may carry
// relay replies (a *textproto.Error), which often echo the recipient address,
// so callers that persist or expose errors should report Stage and Code()
// rather than Error().
type SendError struct {
	// Stage is one of: dial, greet, starttls, auth, mail from, rcpt to, data,
	// write body, close body, quit, set deadline.
	Stage string
	// Addr is the relay host:port for dial/greet failures.
	Addr string
	Err  error
}

func (e *SendError) Error() string {
	if e.Addr != "" {
		return fmt.Sprintf("smtp: %s %s: %v", e.Stage, e.Addr, e.Err)
	}
	return fmt.Sprintf("smtp: %s: %v", e.Stage, e.Err)
}

func (e *SendError) Unwrap() error { return e.Err }

// Code returns the SMTP reply code of the failure, or 0 when the failure was
// not a relay reply (network error, TLS failure, ...).
func (e *SendError) Code() int {
	var tp *textproto.Error
	if errors.As(e.Err, &tp) {
		return tp.Code
	}
	return 0
}

// Permanent reports a 5xx relay reply: retrying the same message cannot succeed.
func (e *SendError) Permanent() bool {
	code := e.Code()
	return code >= 500 && code < 600
}

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
		return fmt.Errorf("%w: %v", ErrInvalidRecipient, err)
	}

	msg := s.buildMessage(recipient.Address, subject, body)
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))

	conn, err := net.DialTimeout("tcp", addr, s.Timeout)
	if err != nil {
		return &SendError{Stage: "dial", Addr: addr, Err: err}
	}
	// One deadline for the entire exchange, refreshed by nothing: a relay that
	// stalls mid-conversation fails rather than hanging.
	if err := conn.SetDeadline(time.Now().Add(s.Timeout)); err != nil {
		_ = conn.Close()
		return &SendError{Stage: "set deadline", Err: err}
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
		return &SendError{Stage: "greet", Addr: addr, Err: err}
	}
	defer func() { _ = client.Close() }()

	if !s.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return &SendError{Stage: "starttls", Err: err}
			}
		}
	}

	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return &SendError{Stage: "auth", Err: err}
		}
	}

	if err := client.Mail(s.fromAddress()); err != nil {
		return &SendError{Stage: "mail from", Err: err}
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return &SendError{Stage: "rcpt to", Err: err}
	}

	writer, err := client.Data()
	if err != nil {
		return &SendError{Stage: "data", Err: err}
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return &SendError{Stage: "write body", Err: err}
	}
	if err := writer.Close(); err != nil {
		return &SendError{Stage: "close body", Err: err}
	}

	if err := client.Quit(); err != nil {
		return &SendError{Stage: "quit", Err: err}
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
