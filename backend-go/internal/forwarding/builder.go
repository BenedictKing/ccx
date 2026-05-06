package forwarding

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

const defaultContentType = "application/json"

type AuthKind string

const (
	AuthKindStandard AuthKind = "standard"
	AuthKindGemini   AuthKind = "gemini"
	AuthKindNone     AuthKind = "none"
)

type ForwardingRequest struct {
	Method          string
	URL             string
	Body            []byte
	ContentType     string
	ServiceType     string
	PlatformHeaders map[string]string
	CustomHeaders   map[string]string
	AuthKind        AuthKind
	APIKey          string
	RawResponse     bool
	RawStream       bool
}

type PreparedRequest struct {
	Request     *http.Request
	Body        []byte
	RawResponse bool
	RawStream   bool
}

func Build(c *gin.Context, input ForwardingRequest) (*PreparedRequest, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("forwarding build requires inbound request context")
	}

	method := strings.TrimSpace(input.Method)
	if method == "" {
		method = c.Request.Method
	}
	if method == "" {
		return nil, fmt.Errorf("forwarding build requires method")
	}

	targetURL := strings.TrimSpace(input.URL)
	if targetURL == "" {
		return nil, fmt.Errorf("forwarding build requires URL")
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), method, targetURL, bodyReader(input.Body))
	if err != nil {
		return nil, fmt.Errorf("create forwarding request: %w", err)
	}

	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" {
		contentType = defaultContentType
	}
	req.Header = utils.PrepareUpstreamHeadersWithContentType(c, req.URL.Host, contentType)
	utils.ApplyCustomHeaders(req.Header, input.PlatformHeaders)
	utils.ApplyCustomHeaders(req.Header, input.CustomHeaders)
	utils.EnsureCompatibleUserAgent(req.Header, input.ServiceType)

	if err := applyAuthentication(req.Header, input.AuthKind, input.APIKey); err != nil {
		return nil, err
	}

	return &PreparedRequest{
		Request:     req,
		Body:        append([]byte(nil), input.Body...),
		RawResponse: input.RawResponse,
		RawStream:   input.RawStream,
	}, nil
}

func bodyReader(body []byte) io.Reader {
	if len(body) == 0 {
		return nil
	}
	return bytes.NewReader(body)
}

func applyAuthentication(headers http.Header, kind AuthKind, apiKey string) error {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}

	switch kind {
	case "", AuthKindStandard:
		utils.SetAuthenticationHeader(headers, apiKey)
	case AuthKindGemini:
		utils.SetGeminiAuthenticationHeader(headers, apiKey)
	case AuthKindNone:
		return nil
	default:
		return fmt.Errorf("unsupported forwarding auth kind %q", kind)
	}

	return nil
}
