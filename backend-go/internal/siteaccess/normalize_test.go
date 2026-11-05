package siteaccess

import "testing"

func TestNormalizeSiteURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://relay.example.com", "https://relay.example.com"},
		{"https://relay.example.com/", "https://relay.example.com"},
		{"https://relay.example.com/keys", "https://relay.example.com"},
		{"https://relay.example.com/console/token", "https://relay.example.com"},
		{"https://relay.example.com/v1", "https://relay.example.com"},
		{"https://relay.example.com/v1/chat/completions", "https://relay.example.com"},
		{"relay.example.com", "https://relay.example.com"},
		{"  https://relay.example.com/  ", "https://relay.example.com"},
		{"https://relay.example.com:8080/keys?tab=all", "https://relay.example.com:8080"},
	}
	for _, c := range cases {
		if got := NormalizeSiteURL(c.in); got != c.want {
			t.Errorf("NormalizeSiteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
