package utils

import (
	"net/url"
	"testing"
)

func TestForwardRequestQuery(t *testing.T) {
	target, _ := url.Parse("https://upstream/models/m:streamGenerateContent?alt=sse")
	source, _ := url.Parse("http://gateway/native?alt=json&%6bey=gateway-secret&future=a%20b&future=c")
	ForwardRequestQuery(target, source)
	if target.RawQuery != "alt=sse&future=a%20b&future=c" {
		t.Fatalf("query changed or gateway credential leaked: %s", target.RawQuery)
	}
}
