package httpconnect

import "testing"

func TestCanonicalTarget(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		defaultPort string
		want        string
		wantErr     bool
	}{
		{name: "host without port", input: "example.com", defaultPort: "443", want: "example.com:443"},
		{name: "host with port", input: "example.com:8443", defaultPort: "443", want: "example.com:8443"},
		{name: "url", input: "https://example.com/path", defaultPort: "443", want: "example.com:443"},
		{name: "url with port", input: "http://example.com:8080/path", defaultPort: "80", want: "example.com:8080"},
		{name: "ipv6", input: "[2001:db8::1]:443", defaultPort: "443", want: "[2001:db8::1]:443"},
		{name: "missing host", input: "", defaultPort: "443", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalTarget(tt.input, tt.defaultPort)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalTarget returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("canonicalTarget() = %q, want %q", got, tt.want)
			}
		})
	}
}
