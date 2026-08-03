package utils

import (
	"reflect"
	"testing"
)

func TestBaseURLSiteIdentities(t *testing.T) {
	tests := []struct {
		name       string
		left       string
		right      string
		equivalent bool
	}{
		{name: "default v1", left: "HTTPS://API.EXAMPLE/v1/", right: "https://api.example", equivalent: true},
		{name: "default v1beta", left: "https://api.example/v1beta/", right: "https://api.example", equivalent: true},
		{name: "tenant path", left: "https://api.example/tenant-a/v1", right: "https://api.example/tenant-b/v1"},
		{name: "port", left: "https://api.example:8443/v1", right: "https://api.example/v1"},
		{name: "query", left: "https://api.example/v1?tenant=a", right: "https://api.example/v1?tenant=b"},
		{name: "hash", left: "https://api.example/v1#", right: "https://api.example/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := BaseURLSiteIdentities(tt.left)
			right := BaseURLSiteIdentities(tt.right)
			matches := false
			for _, a := range left {
				for _, b := range right {
					if a == b {
						matches = true
					}
				}
			}
			if matches != tt.equivalent {
				t.Fatalf("站点等价性错误: left=%v right=%v equivalent=%v", left, right, matches)
			}
		})
	}
}

func TestBaseURLSiteIdentitiesStable(t *testing.T) {
	first := BaseURLSiteIdentities("https://api.example/v1")
	second := BaseURLSiteIdentities("https://api.example/v1")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("站点身份不稳定: first=%v second=%v", first, second)
	}
}
