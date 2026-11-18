package common

import (
	"fmt"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

const retryBudgetContextKey = "ccxRetryBudget"

type retryBudget struct {
	remaining int
	deadline  time.Time
}

func requestRetryBudget(c *gin.Context) *retryBudget {
	v, _ := c.Get(retryBudgetContextKey)
	b, _ := v.(*retryBudget)
	return b
}
func (b *retryBudget) consume() error {
	if b == nil {
		return nil
	}
	if b.remaining <= 0 {
		return fmt.Errorf("已达到本次请求的最大上游尝试次数")
	}
	if !time.Now().Before(b.deadline) {
		return fmt.Errorf("本次请求的重试窗口已结束")
	}
	b.remaining--
	return nil
}

func exactTargetFilter(target string, allowSubagentOverride bool, previous scheduler.CandidateFilterFunc) scheduler.CandidateFilterFunc {
	return func(channels []scheduler.ChannelInfo, getUpstream func(scheduler.ChannelInfo) *config.UpstreamConfig, available func(scheduler.ChannelInfo, *config.UpstreamConfig) bool) ([]scheduler.ChannelInfo, error) {
		if previous != nil {
			var err error
			channels, err = previous(channels, getUpstream, available)
			if err != nil {
				return nil, err
			}
		}
		out := make([]scheduler.ChannelInfo, 0, len(channels))
		for _, channel := range channels {
			upstream := getUpstream(channel)
			if upstream == nil {
				continue
			}
			if allowSubagentOverride && strings.TrimSpace(upstream.SubagentModel) != "" {
				out = append(out, channel)
				continue
			}
			if config.RedirectModel(target, upstream) == target {
				out = append(out, channel)
			}
		}
		return out, nil
	}
}
