package config

import (
	"fmt"
	"strings"
)

type ResolvedEmbeddingCapability struct {
	Capability     EmbeddingCapability
	RequestModel   string
	ActualModel    string
	MatchedPattern string
	Known          bool
}

func ResolveEmbeddingCapability(requestModel string, upstream *UpstreamConfig) ResolvedEmbeddingCapability {
	actualModel := strings.TrimSpace(requestModel)
	if upstream == nil {
		return ResolvedEmbeddingCapability{RequestModel: requestModel, ActualModel: actualModel}
	}

	if capability, pattern, ok := resolvePatternValueFold(actualModel, upstream.EmbeddingCapabilities); ok {
		return ResolvedEmbeddingCapability{
			Capability:     capability,
			RequestModel:   requestModel,
			ActualModel:    actualModel,
			MatchedPattern: pattern,
			Known:          true,
		}
	}

	return ResolvedEmbeddingCapability{RequestModel: requestModel, ActualModel: actualModel}
}

func ValidateEmbeddingCapabilities(capabilities map[string]EmbeddingCapability) error {
	for pattern, capability := range capabilities {
		trimmedPattern := strings.TrimSpace(pattern)
		if trimmedPattern == "" {
			return &ConfigError{
				Message: "embeddingCapabilities 包含空模型匹配规则",
				Cause:   ErrInvalidEmbeddingCapability,
			}
		}
		if capability.Dimensions < 0 {
			return &ConfigError{
				Message: fmt.Sprintf("embeddingCapabilities[%q].dimensions 不能为负数", trimmedPattern),
				Cause:   ErrInvalidEmbeddingCapability,
			}
		}
		for _, dimension := range capability.SupportedDimensions {
			if dimension <= 0 {
				return &ConfigError{
					Message: fmt.Sprintf("embeddingCapabilities[%q].supportedDimensions 仅支持正整数", trimmedPattern),
					Cause:   ErrInvalidEmbeddingCapability,
				}
			}
		}
	}
	return nil
}
