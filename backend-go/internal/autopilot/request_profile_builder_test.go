package autopilot

import (
	"context"
	"testing"
)

func TestBuildRequestProfile(t *testing.T) {
	tests := []struct {
		name       string
		features   RequestProfileFeatures
		taskClass  TaskClass
		quality    QualityTier
		context    int
		visionNeed bool
	}{
		{
			name:      "未知输入保守归类为 supervisor",
			features:  RequestProfileFeatures{Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion"},
			taskClass: TaskClassSupervisor,
			quality:   QualityTierHigh,
		},
		{
			name:      "明确的小型文本请求归类为 lightweight",
			features:  RequestProfileFeatures{Model: "mimo-v2.5-pro", ChannelKind: "messages", Operation: "completion", EstTokens: 500, Complexity: TaskComplexityTrivial},
			taskClass: TaskClassLightweight,
			quality:   QualityTierHigh,
			context:   500,
		},
		{
			name:       "图片请求强制 vision 能力",
			features:   RequestProfileFeatures{Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion", HasImage: true, EstTokens: 1000},
			taskClass:  TaskClassVision,
			quality:    QualityTierHigh,
			context:    1000,
			visionNeed: true,
		},
		{
			name:      "工具请求不允许降为 lightweight",
			features:  RequestProfileFeatures{Model: "glm-5.2", ChannelKind: "chat", Operation: "completion", EstTokens: 500, ToolUseNeed: true},
			taskClass: TaskClassSupervisor,
			quality:   QualityTierPremium,
			context:   500,
		},
		{
			name:      "images 端点优先归类",
			features:  RequestProfileFeatures{Model: "gpt-image-2", ChannelKind: "images", Operation: "image_generation", ImageGenNeed: true},
			taskClass: TaskClassImageGen,
			quality:   QualityTierNormal,
		},
		{
			name:      "vectors 端点优先归类",
			features:  RequestProfileFeatures{Model: "text-embedding-3-small", ChannelKind: "vectors", Operation: "embedding", EmbeddingNeed: true, EmbeddingDimension: 1536},
			taskClass: TaskClassEmbedding,
			quality:   QualityTierLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := BuildRequestProfile(tt.features)
			if profile.TaskClass != tt.taskClass {
				t.Fatalf("TaskClass = %q, want %q", profile.TaskClass, tt.taskClass)
			}
			if profile.QualityNeed != tt.quality {
				t.Fatalf("QualityNeed = %q, want %q", profile.QualityNeed, tt.quality)
			}
			if profile.ContextNeed != tt.context {
				t.Fatalf("ContextNeed = %d, want %d", profile.ContextNeed, tt.context)
			}
			if profile.VisionNeed != tt.visionNeed {
				t.Fatalf("VisionNeed = %v, want %v", profile.VisionNeed, tt.visionNeed)
			}
		})
	}
}

func TestRequestProfileContextStoresValueCopy(t *testing.T) {
	original := RequestProfile{Model: "claude-sonnet-5", TaskClass: TaskClassSupervisor}
	ctx := ContextWithRequestProfile(context.Background(), original)

	first, ok := RequestProfileFromContext(ctx)
	if !ok {
		t.Fatal("RequestProfileFromContext() did not find profile")
	}
	first.Model = "mutated"

	second, ok := RequestProfileFromContext(ctx)
	if !ok || second.Model != original.Model {
		t.Fatalf("stored profile mutated: got %q, want %q", second.Model, original.Model)
	}
}

func TestBuildRequestProfileDocumentNeed(t *testing.T) {
	// HasDocument 或显式 DocumentNeed 都应推导出 DocumentNeed
	withDoc := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion", HasDocument: true,
	})
	if !withDoc.DocumentNeed || !withDoc.HasDocument {
		t.Fatalf("HasDocument=true should produce DocumentNeed, got DocumentNeed=%v HasDocument=%v",
			withDoc.DocumentNeed, withDoc.HasDocument)
	}

	explicit := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion", DocumentNeed: true,
	})
	if !explicit.DocumentNeed {
		t.Fatal("explicit DocumentNeed=true should be preserved")
	}

	withoutDoc := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion",
	})
	if withoutDoc.DocumentNeed || withoutDoc.HasDocument {
		t.Fatalf("no document should not produce DocumentNeed, got DocumentNeed=%v HasDocument=%v",
			withoutDoc.DocumentNeed, withoutDoc.HasDocument)
	}
}
