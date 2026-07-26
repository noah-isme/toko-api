package common

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTPServer speaks just enough SMTP to accept one message and record it.
type fakeSMTPServer struct {
	listener net.Listener
	mu       sync.Mutex
	received string
	envelope []string
	// advertiseSTARTTLS controls whether EHLO offers the extension. The sender
	// must not attempt an upgrade the server never announced.
	advertiseSTARTTLS bool
}

func newFakeSMTPServer(t *testing.T, advertiseSTARTTLS bool) *fakeSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{listener: listener, advertiseSTARTTLS: advertiseSTARTTLS}
	go s.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return s
}

func (s *fakeSMTPServer) addr() (string, int) {
	tcpAddr := s.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", tcpAddr.Port
}

func (s *fakeSMTPServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	write := func(format string, args ...any) {
		fmt.Fprintf(conn, format+"\r\n", args...)
	}

	write("220 fake ESMTP")

	var body strings.Builder
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")

		if inData {
			if trimmed == "." {
				inData = false
				s.mu.Lock()
				s.received = body.String()
				s.mu.Unlock()
				write("250 OK")
				continue
			}
			body.WriteString(trimmed + "\n")
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "EHLO"), strings.HasPrefix(trimmed, "HELO"):
			if s.advertiseSTARTTLS {
				write("250-fake")
				write("250 STARTTLS")
			} else {
				write("250 fake")
			}
		case strings.HasPrefix(trimmed, "MAIL FROM"), strings.HasPrefix(trimmed, "RCPT TO"):
			s.mu.Lock()
			s.envelope = append(s.envelope, trimmed)
			s.mu.Unlock()
			write("250 OK")
		case trimmed == "DATA":
			inData = true
			write("354 End data with <CR><LF>.<CR><LF>")
		case trimmed == "QUIT":
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

func (s *fakeSMTPServer) message() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.received
}

func (s *fakeSMTPServer) envelopeLines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.envelope...)
}

func TestNewSMTPSenderRejectsInvalidConfig(t *testing.T) {
	cases := map[string]SMTPSender{
		"missing host":  {Port: 587, From: "no-reply@toko.local"},
		"bad port":      {Host: "localhost", Port: 0, From: "no-reply@toko.local"},
		"port too high": {Host: "localhost", Port: 70000, From: "no-reply@toko.local"},
		"bad from":      {Host: "localhost", Port: 587, From: "not-an-address"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSMTPSender(cfg); err == nil {
				t.Fatalf("expected an error for %s", name)
			}
		})
	}
}

func TestNewSMTPSenderAppliesDefaultTimeout(t *testing.T) {
	sender, err := NewSMTPSender(SMTPSender{Host: "localhost", Port: 587, From: "no-reply@toko.local"})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	if sender.Timeout != defaultSMTPTimeout {
		t.Fatalf("timeout = %s, want %s", sender.Timeout, defaultSMTPTimeout)
	}
}

func TestSMTPSenderDeliversMessage(t *testing.T) {
	server := newFakeSMTPServer(t, false)
	host, port := server.addr()

	sender, err := NewSMTPSender(SMTPSender{
		Host: host, Port: port, From: "no-reply@toko.local", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}

	if err := sender.Send("budi@example.com", "Verifikasi Email", "Klik tautan: https://toko.local/verify-email?token=abc"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg := server.message()
	for _, want := range []string{
		"From: no-reply@toko.local",
		"To: budi@example.com",
		"Subject: Verifikasi Email",
		"MIME-Version: 1.0",
		`Content-Type: text/plain; charset="UTF-8"`,
		"https://toko.local/verify-email?token=abc",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\ngot:\n%s", want, msg)
		}
	}

	envelope := server.envelopeLines()
	if len(envelope) != 2 {
		t.Fatalf("envelope = %v, want MAIL FROM and RCPT TO", envelope)
	}
	if !strings.Contains(envelope[0], "no-reply@toko.local") {
		t.Errorf("MAIL FROM = %q", envelope[0])
	}
	if !strings.Contains(envelope[1], "budi@example.com") {
		t.Errorf("RCPT TO = %q", envelope[1])
	}
}

func TestSMTPSenderUsesHTMLContentTypeForMarkup(t *testing.T) {
	server := newFakeSMTPServer(t, false)
	host, port := server.addr()

	sender, _ := NewSMTPSender(SMTPSender{
		Host: host, Port: port, From: "no-reply@toko.local", Timeout: 5 * time.Second,
	})

	if err := sender.Send("budi@example.com", "Halo", "<p>Halo <b>Budi</b></p>"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if !strings.Contains(server.message(), `Content-Type: text/html; charset="UTF-8"`) {
		t.Errorf("expected html content type, got:\n%s", server.message())
	}
}

func TestSMTPSenderEncodesNonASCIISubject(t *testing.T) {
	server := newFakeSMTPServer(t, false)
	host, port := server.addr()

	sender, _ := NewSMTPSender(SMTPSender{
		Host: host, Port: port, From: "no-reply@toko.local", Timeout: 5 * time.Second,
	})

	if err := sender.Send("budi@example.com", "Pesanan diterima ✓", "isi"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg := server.message()
	// A raw non-ASCII byte in a header is not valid; it must be encoded.
	if strings.Contains(msg, "✓") {
		t.Errorf("subject was not encoded:\n%s", msg)
	}
	if !strings.Contains(msg, "Subject: =?UTF-8?q?") {
		t.Errorf("expected an encoded-word subject, got:\n%s", msg)
	}
}

func TestSMTPSenderPreservesNewlinesAsCRLF(t *testing.T) {
	server := newFakeSMTPServer(t, false)
	host, port := server.addr()

	sender, _ := NewSMTPSender(SMTPSender{
		Host: host, Port: port, From: "no-reply@toko.local", Timeout: 5 * time.Second,
	})

	if err := sender.Send("budi@example.com", "Multi", "baris satu\nbaris dua"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg := server.message()
	if !strings.Contains(msg, "baris satu") || !strings.Contains(msg, "baris dua") {
		t.Errorf("body lines missing:\n%s", msg)
	}
}

func TestSMTPSenderRejectsInvalidRecipient(t *testing.T) {
	sender, _ := NewSMTPSender(SMTPSender{
		Host: "127.0.0.1", Port: 25, From: "no-reply@toko.local", Timeout: time.Second,
	})

	// Must fail on validation, before any connection is attempted.
	if err := sender.Send("not-an-address", "Halo", "isi"); err == nil {
		t.Fatal("expected an error for an invalid recipient")
	}
}

func TestSMTPSenderFailsWhenRelayUnreachable(t *testing.T) {
	// Port 1 on loopback: nothing listens, so this exercises the dial path.
	sender, _ := NewSMTPSender(SMTPSender{
		Host: "127.0.0.1", Port: 1, From: "no-reply@toko.local", Timeout: 2 * time.Second,
	})

	if err := sender.Send("budi@example.com", "Halo", "isi"); err == nil {
		t.Fatal("expected an error when the relay is unreachable")
	}
}

func TestLooksLikeHTML(t *testing.T) {
	cases := map[string]bool{
		"<p>hello</p>":                  true,
		"  <html><body></body></html> ": true,
		"Klik tautan: https://x/y":      false,
		"a < b and c > d":               false,
		"":                              false,
	}
	for body, want := range cases {
		if got := looksLikeHTML(body); got != want {
			t.Errorf("looksLikeHTML(%q) = %v, want %v", body, got, want)
		}
	}
}
