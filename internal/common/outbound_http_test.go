package common

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRejectUnsafeAddr(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:443", "10.1.2.3:443", "172.16.0.1:443", "192.168.1.1:443", "169.254.169.254:80",
		"100.64.0.1:443", "0.0.0.0:443", "[::1]:443", "[fd00::1]:443", "[fe80::1]:443",
		"[::ffff:127.0.0.1]:443", "[::ffff:10.0.0.1]:443", "224.0.0.1:443", "not-an-ip:443", "garbage",
		"192.0.0.8:443", "198.18.0.1:443", "198.19.255.254:443", "240.0.0.1:443", "255.255.255.255:443",
		"[64:ff9b::a00:1]:443", "[64:ff9b:1::1]:443", "[2002:a00:1::1]:443", "[2001:0:4136:e378::1]:443",
	} {
		if err := RejectUnsafeAddr("tcp", addr, nil); !errors.Is(err, ErrUnsafeAddress) {
			t.Errorf("RejectUnsafeAddr(%q) = %v, want ErrUnsafeAddress", addr, err)
		}
	}
	for _, addr := range []string{"93.184.216.34:443", "[2606:2800:220:1:248:1893:25c8:1946]:443", "142.250.0.1:443"} {
		if err := RejectUnsafeAddr("tcp", addr, nil); err != nil {
			t.Errorf("RejectUnsafeAddr(%q) = %v, want nil", addr, err)
		}
	}
}

func TestOutboundClientRefusesLoopbackDial(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	client := NewOutboundHTTPClient(2*time.Second, false)
	resp, err := client.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected the loopback dial to be refused")
	}
	if !errors.Is(err, ErrUnsafeAddress) {
		t.Fatalf("expected ErrUnsafeAddress, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("handler must never be invoked")
	}
}

func TestOutboundClientDoesNotFollowRedirect(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	client := NewOutboundHTTPClient(2*time.Second, true)
	resp, err := client.Post(redirector.URL, "application/octet-stream", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", resp.StatusCode)
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target must never be invoked")
	}
}
