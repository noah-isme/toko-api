package push

import (
	"errors"
	"strings"
	"testing"

	"github.com/noah-isme/backend-toko/internal/config"
)

func TestValidateEndpointAccepts(t *testing.T) {
	for _, ep := range []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/x",
		"https://wns2-bn1p.notify.windows.com/w/?token=y",
		"https://web.push.apple.com/QWxhZGRpbg",
		"https://FCM.googleapis.com/fcm/send/abc",
	} {
		if err := ValidateEndpoint(ep, config.DefaultPushEndpointAllowedHosts); err != nil {
			t.Errorf("ValidateEndpoint(%q) = %v", ep, err)
		}
	}
}

func TestValidateEndpointRejects(t *testing.T) {
	for _, ep := range []string{
		"http://fcm.googleapis.com/x",
		"https://10.0.0.1/x",
		"https://[::1]/x",
		"https://127.0.0.1:8080/x",
		"https://user:pw@fcm.googleapis.com/x",
		"https://evil.example.com/x",
		"https://fcm.googleapis.com.evil.example/x",
		"https://push.services.mozilla.com/x", // suffix entries need a subdomain
		"fcm.googleapis.com/x",
		"",
	} {
		if err := ValidateEndpoint(ep, config.DefaultPushEndpointAllowedHosts); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("ValidateEndpoint(%q) = %v, want ErrInvalidEndpoint", ep, err)
		}
	}
}

func TestValidateEndpointWildcardSkipsHostAllowlist(t *testing.T) {
	all := []string{"*"}
	if err := ValidateEndpoint("https://evil.example.com/x", all); err != nil {
		t.Fatalf("wildcard should accept any DNS host: %v", err)
	}
	for _, ep := range []string{"http://evil.example.com/x", "https://10.0.0.1/x", "https://[fd00::1]/x"} {
		if err := ValidateEndpoint(ep, all); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("ValidateEndpoint(%q, *) = %v, want ErrInvalidEndpoint", ep, err)
		}
	}
}

func TestSendableEndpointRejectsHTTP(t *testing.T) {
	if err := SendableEndpoint("http://10.0.0.5/x"); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("expected ErrInvalidEndpoint, got %v", err)
	}
	if err := SendableEndpoint("https://fcm.googleapis.com/x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEndpointErrorOmitsURL(t *testing.T) {
	err := ValidateEndpoint("https://evil.example.com/secret-token", config.DefaultPushEndpointAllowedHosts)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, leak := range []string{"secret-token", "evil.example.com"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaks %q: %v", leak, err)
		}
	}
}
