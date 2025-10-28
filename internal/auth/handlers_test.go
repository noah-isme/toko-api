package auth

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		want       string
	}{
		{
			name:       "forwarded",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.1, 70.41.3.18"},
			remoteAddr: "192.0.2.1:1234",
			want:       "203.0.113.1",
		},
		{
			name:       "real ip",
			headers:    map[string]string{"X-Real-IP": "198.51.100.2"},
			remoteAddr: "192.0.2.1:1234",
			want:       "198.51.100.2",
		},
		{
			name:       "remote addr fallback",
			headers:    map[string]string{},
			remoteAddr: "198.51.100.3:8080",
			want:       "198.51.100.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			req.RemoteAddr = tt.remoteAddr
			if got := clientIP(req); got != tt.want {
				t.Fatalf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
