package config

import (
	"errors"
	"testing"
)

func TestResolveEmbeddingCapability(t *testing.T) {
	normalizedTrue := true
	normalizedFalse := false

	tests := []struct {
		name             string
		requestModel     string
		upstream         *UpstreamConfig
		wantKnown        bool
		wantActualModel  string
		wantMatchPattern string
		wantSpaceID      string
		wantDimensions   int
		wantNormalized   *bool
	}{
		{
			name:            "nil capabilities returns Known=false",
			requestModel:    "some-model",
			upstream:        &UpstreamConfig{EmbeddingCapabilities: nil},
			wantKnown:       false,
			wantActualModel: "some-model",
		},
		{
			name:            "empty capabilities returns Known=false",
			requestModel:    "some-model",
			upstream:        &UpstreamConfig{EmbeddingCapabilities: map[string]EmbeddingCapability{}},
			wantKnown:       false,
			wantActualModel: "some-model",
		},
		{
			name:         "no pattern match returns Known=false",
			requestModel: "unrelated-model",
			upstream: &UpstreamConfig{
				EmbeddingCapabilities: map[string]EmbeddingCapability{
					"text-embedding-3-*": {Dimensions: 1536, Normalized: &normalizedTrue},
				},
			},
			wantKnown:       false,
			wantActualModel: "unrelated-model",
		},
		{
			name:            "nil upstream returns Known=false",
			requestModel:    "some-model",
			upstream:        nil,
			wantKnown:       false,
			wantActualModel: "some-model",
		},
		{
			name:         "exact non-wildcard match",
			requestModel: "text-embedding-3-small",
			upstream: &UpstreamConfig{
				EmbeddingCapabilities: map[string]EmbeddingCapability{
					"text-embedding-3-small": {
						EmbeddingSpaceID:    "openai-small",
						Dimensions:          1536,
						SupportedDimensions: []int{512, 1024, 1536},
						Normalized:          &normalizedFalse,
					},
				},
			},
			wantKnown:        true,
			wantActualModel:  "text-embedding-3-small",
			wantMatchPattern: "text-embedding-3-small",
			wantSpaceID:      "openai-small",
			wantDimensions:   1536,
			wantNormalized:   &normalizedFalse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := ResolveEmbeddingCapability(tt.requestModel, tt.upstream)
			if resolved.Known != tt.wantKnown {
				t.Fatalf("Known = %v, want %v", resolved.Known, tt.wantKnown)
			}
			if resolved.ActualModel != tt.wantActualModel {
				t.Fatalf("ActualModel = %q, want %q", resolved.ActualModel, tt.wantActualModel)
			}
			if resolved.RequestModel != tt.requestModel {
				t.Fatalf("RequestModel = %q, want %q", resolved.RequestModel, tt.requestModel)
			}
			if tt.wantKnown {
				if resolved.MatchedPattern != tt.wantMatchPattern {
					t.Fatalf("MatchedPattern = %q, want %q", resolved.MatchedPattern, tt.wantMatchPattern)
				}
				if resolved.Capability.EmbeddingSpaceID != tt.wantSpaceID {
					t.Fatalf("EmbeddingSpaceID = %q, want %q", resolved.Capability.EmbeddingSpaceID, tt.wantSpaceID)
				}
				if resolved.Capability.Dimensions != tt.wantDimensions {
					t.Fatalf("Dimensions = %d, want %d", resolved.Capability.Dimensions, tt.wantDimensions)
				}
				if tt.wantNormalized == nil {
					if resolved.Capability.Normalized != nil {
						t.Fatalf("Normalized = %v, want nil", *resolved.Capability.Normalized)
					}
				} else {
					if resolved.Capability.Normalized == nil || *resolved.Capability.Normalized != *tt.wantNormalized {
						t.Fatalf("Normalized = %v, want %v", resolved.Capability.Normalized, *tt.wantNormalized)
					}
				}
				if len(tt.upstream.EmbeddingCapabilities[tt.wantMatchPattern].SupportedDimensions) > 0 {
					if len(resolved.Capability.SupportedDimensions) != len(tt.upstream.EmbeddingCapabilities[tt.wantMatchPattern].SupportedDimensions) {
						t.Fatalf("SupportedDimensions length = %d, want %d",
							len(resolved.Capability.SupportedDimensions),
							len(tt.upstream.EmbeddingCapabilities[tt.wantMatchPattern].SupportedDimensions))
					}
				}
			}
		})
	}
}

func TestValidateEmbeddingCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		capabilities map[string]EmbeddingCapability
		wantErr      bool
	}{
		{
			name: "nil capabilities",
		},
		{
			name: "valid dimensions",
			capabilities: map[string]EmbeddingCapability{
				"text-embedding-3-small": {Dimensions: 1536, SupportedDimensions: []int{512, 1536}},
			},
		},
		{
			name: "empty pattern",
			capabilities: map[string]EmbeddingCapability{
				" ": {Dimensions: 1536},
			},
			wantErr: true,
		},
		{
			name: "negative dimensions",
			capabilities: map[string]EmbeddingCapability{
				"text-embedding-3-small": {Dimensions: -1},
			},
			wantErr: true,
		},
		{
			name: "zero supported dimensions",
			capabilities: map[string]EmbeddingCapability{
				"text-embedding-3-small": {Dimensions: 1536, SupportedDimensions: []int{0, 1536}},
			},
			wantErr: true,
		},
		{
			name: "negative supported dimensions",
			capabilities: map[string]EmbeddingCapability{
				"text-embedding-3-small": {Dimensions: 1536, SupportedDimensions: []int{-1, 1536}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmbeddingCapabilities(tt.capabilities)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidEmbeddingCapability) {
					t.Fatalf("ValidateEmbeddingCapabilities() error = %v, want ErrInvalidEmbeddingCapability", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateEmbeddingCapabilities() error = %v", err)
			}
		})
	}
}
