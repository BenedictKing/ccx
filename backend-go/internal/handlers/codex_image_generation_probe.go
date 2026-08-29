package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/utils"
)

const (
	codexAutoReviewModel                  = "codex-auto-review"
	codexImageGenerationRestrictionReason = "image_generation_not_enabled"
)

type ImageGenerationProbeState string

const (
	ImageGenerationProbeSupported    ImageGenerationProbeState = "supported"
	ImageGenerationProbeUnsupported  ImageGenerationProbeState = "unsupported"
	ImageGenerationProbeInconclusive ImageGenerationProbeState = "inconclusive"
)

type imageGenerationProbeMode string

const (
	imageGenerationProbeHosted    imageGenerationProbeMode = "hosted"
	imageGenerationProbeNamespace imageGenerationProbeMode = "namespace"
)

type imageGenerationModeProbeResult struct {
	Mode       imageGenerationProbeMode
	State      ImageGenerationProbeState
	StatusCode int
	Diagnostic string
	Body       []byte
}

// CodexImageGenerationKeyProbeResult 描述一个 Key 对 Codex 两种图片工具声明的支持状态。
type CodexImageGenerationKeyProbeResult struct {
	KeyMask       string                    `json:"keyMask"`
	HostedTool    ImageGenerationProbeState `json:"hostedTool"`
	NamespaceTool ImageGenerationProbeState `json:"namespaceTool"`
	Status        ImageGenerationProbeState `json:"status"`
}

// CodexImageGenerationProbeSummary 是 codex-auto-review 的 Key×实际模型探测汇总。
type CodexImageGenerationProbeSummary struct {
	Tested             bool                                 `json:"tested"`
	Supported          bool                                 `json:"supported"`
	CompatibleViaStrip bool                                 `json:"compatibleViaStrip,omitempty"`
	ActualModel        string                               `json:"actualModel"`
	SupportedKeys      int                                  `json:"supportedKeys"`
	UnsupportedKeys    int                                  `json:"unsupportedKeys"`
	InconclusiveKeys   int                                  `json:"inconclusiveKeys"`
	KeyResults         []CodexImageGenerationKeyProbeResult `json:"keyResults,omitempty"`
}

func shouldProbeCodexImageGeneration(protocol, model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), codexAutoReviewModel) &&
		targetProtocolForCapabilityProtocol(protocol) == "responses"
}

func imageGenerationProbeModes(protocol string) []imageGenerationProbeMode {
	if protocol == "responses" {
		return []imageGenerationProbeMode{imageGenerationProbeHosted, imageGenerationProbeNamespace}
	}
	if protocol == "chat" {
		return []imageGenerationProbeMode{imageGenerationProbeHosted}
	}
	return nil
}

func probeImageGenerationToolModes(
	ctx context.Context,
	channel *config.UpstreamConfig,
	protocol string,
	apiKey string,
	baseURL string,
	model string,
) []imageGenerationModeProbeResult {
	modes := imageGenerationProbeModes(protocol)
	results := make([]imageGenerationModeProbeResult, 0, len(modes))
	for _, mode := range modes {
		if ctx.Err() != nil {
			results = append(results, imageGenerationModeProbeResult{Mode: mode, State: ImageGenerationProbeInconclusive, Diagnostic: ctx.Err().Error()})
			continue
		}

		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		result := probeImageGenerationToolMode(probeCtx, channel, protocol, apiKey, baseURL, model, mode)
		cancel()
		results = append(results, result)
	}
	return results
}

func probeImageGenerationToolMode(
	ctx context.Context,
	channel *config.UpstreamConfig,
	protocol string,
	apiKey string,
	baseURL string,
	model string,
	mode imageGenerationProbeMode,
) imageGenerationModeProbeResult {
	result := imageGenerationModeProbeResult{Mode: mode, State: ImageGenerationProbeInconclusive}

	var (
		req *http.Request
		err error
	)
	switch protocol {
	case "responses":
		body := buildResponsesImageGenerationToolProbeBody(model)
		if mode == imageGenerationProbeNamespace {
			body = buildResponsesImageGenNamespaceProbeBody(model)
		}
		req, err = buildResponsesCompatRequest(baseURL, body, channel, apiKey)
	case "chat":
		if mode != imageGenerationProbeHosted {
			return result
		}
		req, err = buildOpenAIChatCompatRequest(baseURL, buildOpenAIChatImageGenerationToolProbeBody(model), channel, apiKey)
	default:
		return result
	}
	if err != nil {
		result.Diagnostic = err.Error()
		return result
	}

	events, statusCode, body, sendErr := sendCompatProbe(ctx, req, channel)
	result.StatusCode = statusCode
	result.Body = []byte(body)
	if isCompatProbeTimeout(sendErr, ctx) {
		result.Diagnostic = "timeout"
		return result
	}

	diagnostic := strings.TrimSpace(body)
	if diagnostic == "" {
		diagnostic = strings.TrimSpace(strings.Join(events, "\n"))
	}
	if sendErr != nil && diagnostic == "" {
		diagnostic = sendErr.Error()
	}
	result.Diagnostic = diagnostic

	if sendErr == nil && statusCode >= 200 && statusCode < 300 && hasMeaningfulCompatSSE(events, protocol) {
		result.State = ImageGenerationProbeSupported
		return result
	}
	if isImageGenerationToolUnsupported(statusCode, diagnostic) {
		result.State = ImageGenerationProbeUnsupported
	}
	return result
}

func buildResponsesImageGenNamespaceProbeBody(models ...string) []byte {
	return buildResponsesToolProbeBody(compatProbeModel("gpt-5.4-mini", models...), map[string]interface{}{
		"type":        "namespace",
		"name":        "image_gen",
		"description": "Image generation tools",
		"tools": []map[string]interface{}{{
			"type":        "function",
			"name":        "imagegen",
			"description": "Generate an image",
			"strict":      false,
			"parameters": map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{},
				"additionalProperties": true,
			},
		}},
	})
}

func buildResponsesToolProbeBody(model string, tool map[string]interface{}) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model":             model,
		"input":             "Reply with ok without calling tools. Nonce: " + nonce,
		"max_output_tokens": 16,
		"stream":            true,
		"tools":             []map[string]interface{}{tool},
	})
	return body
}

func aggregateImageGenerationProbeState(results []imageGenerationModeProbeResult) ImageGenerationProbeState {
	if len(results) == 0 {
		return ImageGenerationProbeInconclusive
	}
	allSupported := true
	for _, result := range results {
		if result.State == ImageGenerationProbeUnsupported {
			return ImageGenerationProbeUnsupported
		}
		if result.State != ImageGenerationProbeSupported {
			allSupported = false
		}
	}
	if allSupported {
		return ImageGenerationProbeSupported
	}
	return ImageGenerationProbeInconclusive
}

func probeStateForMode(results []imageGenerationModeProbeResult, mode imageGenerationProbeMode) ImageGenerationProbeState {
	for _, result := range results {
		if result.Mode == mode {
			return result.State
		}
	}
	return ImageGenerationProbeInconclusive
}

func executeCodexImageGenerationCapabilityTest(
	ctx context.Context,
	channel *config.UpstreamConfig,
	model string,
	timeout time.Duration,
	cfgManager *config.ConfigManager,
	channelID int,
	channelKind string,
) ModelTestResult {
	startedAt := time.Now()
	actualModel := config.RedirectModel(model, channel)
	result := ModelTestResult{
		Model:       model,
		ActualModel: actualModel,
		StartedAt:   startedAt.Format(time.RFC3339Nano),
	}
	summary := &CodexImageGenerationProbeSummary{ActualModel: actualModel}
	result.CodexImageGeneration = summary

	probeChannel := channel.Clone()
	probeChannel.DisabledKeyModels = nil
	candidates := keypool.CandidatesForModel(probeChannel, nil, actualModel)
	if len(candidates) == 0 {
		errMsg := "no_eligible_api_key"
		result.Error = &errMsg
		result.TestedAt = time.Now().Format(time.RFC3339Nano)
		return result
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, candidate := range candidates {
		if probeCtx.Err() != nil {
			summary.InconclusiveKeys++
			summary.KeyResults = append(summary.KeyResults, CodexImageGenerationKeyProbeResult{
				KeyMask:       utils.MaskAPIKey(candidate.APIKey),
				HostedTool:    ImageGenerationProbeInconclusive,
				NamespaceTool: ImageGenerationProbeInconclusive,
				Status:        ImageGenerationProbeInconclusive,
			})
			continue
		}

		baseURL := codexProbeBaseURL(channel, candidate)
		modeResults := probeImageGenerationToolModes(probeCtx, channel, "responses", candidate.APIKey, baseURL, actualModel)
		state := aggregateImageGenerationProbeState(modeResults)
		summary.Tested = true
		summary.KeyResults = append(summary.KeyResults, CodexImageGenerationKeyProbeResult{
			KeyMask:       utils.MaskAPIKey(candidate.APIKey),
			HostedTool:    probeStateForMode(modeResults, imageGenerationProbeHosted),
			NamespaceTool: probeStateForMode(modeResults, imageGenerationProbeNamespace),
			Status:        state,
		})
		switch state {
		case ImageGenerationProbeSupported:
			summary.SupportedKeys++
		case ImageGenerationProbeUnsupported:
			summary.UnsupportedKeys++
		default:
			summary.InconclusiveKeys++
		}
		reconcileCodexImageGenerationRestriction(cfgManager, channel, channelKind, channelID, candidate.APIKey, actualModel, state, modeResults)
	}

	summary.Supported = summary.SupportedKeys > 0
	result.Latency = time.Since(startedAt).Milliseconds()
	result.TestedAt = time.Now().Format(time.RFC3339Nano)
	if summary.Supported {
		result.Success = true
		result.StreamingSupported = true
		return result
	}

	if channel.IsStripImageGenerationToolEnabled() && probePlainResponsesWithCandidates(probeCtx, channel, candidates, actualModel) {
		summary.CompatibleViaStrip = true
		result.Success = true
		result.StreamingSupported = true
		return result
	}

	errMsg := fmt.Sprintf("codex_image_generation_unavailable: supported=%d unsupported=%d inconclusive=%d",
		summary.SupportedKeys, summary.UnsupportedKeys, summary.InconclusiveKeys)
	result.Error = &errMsg
	return result
}

func codexProbeBaseURL(channel *config.UpstreamConfig, candidate keypool.Candidate) string {
	if baseURL := strings.TrimSpace(candidate.Config.BaseURL); baseURL != "" {
		return baseURL
	}
	if baseURL := strings.TrimSpace(channel.BoundBaseURLForKey(candidate.APIKey)); baseURL != "" {
		return baseURL
	}
	return capabilityTestBaseURL(channel)
}

func probePlainResponsesWithCandidates(ctx context.Context, channel *config.UpstreamConfig, candidates []keypool.Candidate, actualModel string) bool {
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return false
		}
		probeChannel := channel.Clone()
		probeChannel.APIKeys = []string{candidate.APIKey}
		probeChannel.DisabledAPIKeys = nil
		probeChannel.BaseURL = codexProbeBaseURL(channel, candidate)
		probeChannel.BaseURLs = nil
		req, err := buildTestRequestWithModel("responses", probeChannel, actualModel)
		if err != nil {
			continue
		}
		success, _, _, _, _, _ := sendAndCheckStream(ctx, probeChannel, req.WithContext(ctx), "responses")
		if success {
			return true
		}
	}
	return false
}

func reconcileCodexImageGenerationRestriction(
	cfgManager *config.ConfigManager,
	channel *config.UpstreamConfig,
	channelKind string,
	channelID int,
	apiKey string,
	actualModel string,
	state ImageGenerationProbeState,
	modeResults []imageGenerationModeProbeResult,
) {
	if cfgManager == nil || apiKey == "" || actualModel == "" || channelID < 0 {
		return
	}
	apiType := channelKindToApiType(channelKind)
	if state == ImageGenerationProbeUnsupported && !channel.IsStripImageGenerationToolEnabled() {
		diagnostic := codexProbeDiagnostic(modeResults)
		_ = cfgManager.DisableKeyModel(apiType, channelID, apiKey, actualModel, codexImageGenerationRestrictionReason, diagnostic)
		return
	}
	if state == ImageGenerationProbeSupported || channel.IsStripImageGenerationToolEnabled() {
		restoreCodexImageGenerationRestriction(cfgManager, channelKind, channelID, apiKey, actualModel)
		return
	}

	for _, modeResult := range modeResults {
		blacklist := common.ShouldBlacklistKey(modeResult.StatusCode, modeResult.Body)
		if !blacklist.ShouldBlacklist {
			continue
		}
		if common.IsBalanceOrQuotaBlacklistReason(blacklist.Reason) && !channel.IsAutoBlacklistBalanceEnabled() {
			return
		}
		_ = cfgManager.BlacklistKeyWithRecoverAt(apiType, channelID, apiKey, blacklist.Reason, blacklist.Message, blacklist.RecoverAt)
		return
	}
}

func codexProbeDiagnostic(results []imageGenerationModeProbeResult) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if result.State != ImageGenerationProbeUnsupported {
			continue
		}
		diagnostic := truncateCapabilityError(strings.TrimSpace(result.Diagnostic))
		parts = append(parts, fmt.Sprintf("%s HTTP %d: %s", result.Mode, result.StatusCode, diagnostic))
	}
	return strings.Join(parts, " / ")
}

func restoreCodexImageGenerationRestriction(cfgManager *config.ConfigManager, channelKind string, channelID int, apiKey, actualModel string) {
	channel, err := getCapabilityTestChannel(cfgManager, channelKind, channelID)
	if err != nil {
		return
	}
	for _, disabled := range channel.DisabledKeyModels {
		if disabled.Key != apiKey || !strings.EqualFold(strings.TrimSpace(disabled.Model), strings.TrimSpace(actualModel)) {
			continue
		}
		if disabled.Reason != codexImageGenerationRestrictionReason {
			return
		}
		_ = cfgManager.RestoreKeyModel(channelKindToApiType(channelKind), channelID, apiKey, actualModel)
		return
	}
}
