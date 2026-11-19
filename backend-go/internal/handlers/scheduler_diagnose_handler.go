package handlers

import (
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

type schedulerDiagnoseRequest struct {
	UserID             string                               `json:"userId"`
	Model              string                               `json:"model"`
	RoutePrefix        string                               `json:"routePrefix"`
	ChannelName        string                               `json:"channelName"`
	FailedChannels     []int                                `json:"failedChannels"`
	HasImageContent    bool                                 `json:"hasImageContent"`
	AgentRole          string                               `json:"agentRole"`
	AgentConfidence    string                               `json:"agentConfidence"`
	ContextRequirement *schedulerDiagnoseContextRequirement `json:"contextRequirement"`
}

type schedulerDiagnoseContextRequirement struct {
	InputTokens                int  `json:"inputTokens"`
	OutputTokens               int  `json:"outputTokens"`
	RequiredTokens             int  `json:"requiredTokens"`
	MinimumContextWindowTokens int  `json:"minimumContextWindowTokens"`
	ExplicitOutputMax          bool `json:"explicitOutputMax"`
	SkipWindowValidation       bool `json:"skipWindowValidation"`
}

// DiagnoseSchedulerSelection 返回一次不发送上游请求的渠道选择诊断。
func DiagnoseSchedulerSelection(sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sch == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "scheduler unavailable"})
			return
		}

		var req schedulerDiagnoseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		failedChannels := make(map[int]bool, len(req.FailedChannels))
		for _, index := range req.FailedChannels {
			if index >= 0 {
				failedChannels[index] = true
			}
		}

		agentRole := strings.TrimSpace(req.AgentRole)
		agentConfidence := strings.TrimSpace(req.AgentConfidence)
		if agentRole == "subagent" && agentConfidence == "" {
			agentConfidence = "exact"
		}
		cfg := sch.ConfigSnapshot()
		subagentRouting, applySubagentRouting := config.ResolveSubagentRouting(cfg, string(kind), agentRole, agentConfidence)
		contextRequirement := diagnoseContextRequirement(req.ContextRequirement)
		sch.ApplySubagentRoutingProfile(contextRequirement, subagentRouting, applySubagentRouting, cfg)
		result, err := sch.SelectChannelWithOptions(c.Request.Context(), scheduler.SelectionOptions{
			UserID:               strings.TrimSpace(req.UserID),
			FailedChannels:       failedChannels,
			Kind:                 kind,
			Model:                strings.TrimSpace(req.Model),
			RoutePrefix:          strings.TrimSpace(req.RoutePrefix),
			ChannelName:          strings.TrimSpace(req.ChannelName),
			ContextRequirement:   contextRequirement,
			HasImageContent:      req.HasImageContent,
			AgentRole:            agentRole,
			AgentConfidence:      agentConfidence,
			SubagentRouting:      subagentRouting,
			ApplySubagentRouting: applySubagentRouting,
			ConfigSnapshot:       &cfg,
			DryRun:               true,
		})
		if err != nil {
			resp := gin.H{
				"ok":    false,
				"kind":  kind,
				"error": err.Error(),
			}
			if trace, ok := scheduler.SelectionTraceFromError(err); ok {
				resp["summary"] = scheduler.FormatSelectionTraceSummary(trace, 8)
				resp["trace"] = trace
			}
			c.JSON(http.StatusOK, resp)
			return
		}

		trace := result.Trace
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"kind":    kind,
			"reason":  result.Reason,
			"summary": scheduler.FormatSelectionTraceSummary(trace, 8),
			"trace":   trace,
			"selected": gin.H{
				"channelIndex": result.ChannelIndex,
				"channelName":  selectedChannelName(result),
				"serviceType":  selectedServiceType(result),
			},
		})
	}
}

func diagnoseContextRequirement(req *schedulerDiagnoseContextRequirement) *scheduler.ContextRequirement {
	if req == nil {
		return nil
	}
	return &scheduler.ContextRequirement{
		InputTokens:                req.InputTokens,
		OutputTokens:               req.OutputTokens,
		RequiredTokens:             req.RequiredTokens,
		MinimumContextWindowTokens: req.MinimumContextWindowTokens,
		ExplicitOutputMax:          req.ExplicitOutputMax,
		SkipWindowValidation:       req.SkipWindowValidation,
	}
}

func selectedChannelName(result *scheduler.SelectionResult) string {
	if result == nil || result.Upstream == nil {
		return ""
	}
	return result.Upstream.Name
}

func selectedServiceType(result *scheduler.SelectionResult) string {
	if result == nil || result.Upstream == nil {
		return ""
	}
	return result.Upstream.ServiceType
}
