package utils

import (
	"errors"
	"net"
	"testing"
)

func TestValidateBaseURLCloudMetadataIPs(t *testing.T) {
	lookupIP := func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.0.2.10")}, nil
	}
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "IPv4 metadata", rawURL: "http://169.254.169.254/latest/meta-data", wantErr: true},
		{name: "IPv6 metadata", rawURL: "http://[fd00:ec2::254]/latest/meta-data", wantErr: true},
		{name: "expanded IPv6 metadata", rawURL: "http://[FD00:0EC2:0000:0000:0000:0000:0000:0254]/", wantErr: true},
		{name: "mapped IPv4 metadata", rawURL: "http://[::ffff:169.254.169.254]/", wantErr: true},
		{name: "loopback upstream", rawURL: "http://127.0.0.1:11434"},
		{name: "private IPv4 upstream", rawURL: "http://192.168.1.20:8080"},
		{name: "private IPv6 upstream", rawURL: "http://[fd12::1]:11434"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBaseURL(tt.rawURL, lookupIP)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBaseURL() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestValidateBaseURLResolvedMetadataIPs(t *testing.T) {
	tests := []struct {
		name      string
		ips       []net.IP
		lookupErr error
		wantErr   bool
	}{
		{name: "IPv4 metadata", ips: []net.IP{net.ParseIP("169.254.169.254")}, wantErr: true},
		{name: "IPv6 metadata", ips: []net.IP{net.ParseIP("fd00:ec2::254")}, wantErr: true},
		{name: "private upstream", ips: []net.IP{net.ParseIP("10.0.0.8")}},
		{name: "lookup failure preserves existing behavior", lookupErr: errors.New("lookup failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookupIP := func(host string) ([]net.IP, error) {
				if host != "upstream.test" {
					t.Fatalf("lookup host = %q", host)
				}
				return tt.ips, tt.lookupErr
			}
			err := validateBaseURL("https://upstream.test/v1", lookupIP)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBaseURL() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
