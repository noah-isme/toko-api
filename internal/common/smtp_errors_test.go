package common

import (
	"errors"
	"net/textproto"
	"strings"
	"testing"
)

func TestSMTPInvalidRecipientErrorOmitsAddress(t *testing.T) {
	sender, err := NewSMTPSender(SMTPSender{Host: "localhost", Port: 587, From: "no-reply@toko.local"})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	err = sender.Send("not an address alice", "s", "b")
	if !errors.Is(err, ErrInvalidRecipient) {
		t.Fatalf("expected ErrInvalidRecipient, got %v", err)
	}
	if strings.Contains(err.Error(), "alice") {
		t.Fatalf("error leaks the recipient: %v", err)
	}
}

func TestSendErrorCodeAndPermanent(t *testing.T) {
	rcpt := &SendError{Stage: "rcpt to", Err: &textproto.Error{Code: 550, Msg: "5.1.1 <alice@example.com> unknown"}}
	if rcpt.Code() != 550 || !rcpt.Permanent() {
		t.Fatalf("expected permanent 550, got code %d", rcpt.Code())
	}
	busy := &SendError{Stage: "rcpt to", Err: &textproto.Error{Code: 451, Msg: "try later"}}
	if busy.Permanent() {
		t.Fatal("4xx must not be permanent")
	}
	dial := &SendError{Stage: "dial", Addr: "smtp.example.com:587", Err: errors.New("connection refused")}
	if dial.Code() != 0 || dial.Permanent() {
		t.Fatal("network errors have no code and are not permanent")
	}
	if !strings.HasPrefix(dial.Error(), "smtp: dial smtp.example.com:587: ") {
		t.Fatalf("unexpected message %q", dial.Error())
	}
}
