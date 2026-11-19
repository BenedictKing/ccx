package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateDialAddress(t *testing.T) {
	tests := []struct {
		address string
		wantErr bool
	}{
		{address: "169.254.169.254:80", wantErr: true},
		{address: "[fd00:ec2::254]:80", wantErr: true},
		{address: "[::ffff:169.254.169.254]:80", wantErr: true},
		{address: "127.0.0.1:11434"},
		{address: "192.168.1.20:8080"},
		{address: "[fd12::1]:11434"},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			err := validateDialAddress(tt.address)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDialAddress() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestSSRFProtectionBlocksCustomDialer(t *testing.T) {
	var dialCalls atomic.Int32
	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("unexpected dial")
		},
	}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	applySSRFProtection(client, transport)

	resp, err := client.Get("http://169.254.169.254/latest/meta-data")
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "云元数据") {
		t.Fatalf("expected metadata error, got %v", err)
	}
	if dialCalls.Load() != 0 {
		t.Fatalf("dialer called %d times", dialCalls.Load())
	}
}

func TestStandardClientBlocksMetadataRedirect(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "http://[fd00:ec2::254]/latest/meta-data", http.StatusFound)
	}))
	defer upstream.Close()

	manager := &ClientManager{clients: make(map[string]*http.Client)}
	client := manager.NewStandardClient(time.Second, false)
	resp, err := client.Get(upstream.URL)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "云元数据") {
		t.Fatalf("expected metadata redirect error, got %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1", requests.Load())
	}
}

func TestStandardClientAllowsLocalRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	manager := &ClientManager{clients: make(map[string]*http.Client)}
	client := manager.NewStandardClient(time.Second, false)
	resp, err := client.Get(redirector.URL)
	if err != nil {
		t.Fatalf("local redirect failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}
