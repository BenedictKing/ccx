package common

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
)

// BindRawStreamFanout 启动 raw passthrough fan-out goroutine，把上游
// *http.Response.Body 切成 SSE event 后写入 pipeline.AttemptState.RawStreamCh，
// 并在 state 上挂载 cancel/errRef，以便 pipeline 在 retry/failover 前安全清理。
//
// 该桥接函数把 ccx 现有 startRawStreamFanout（基于 rawStreamEvent 的 chan）
// 适配到 pipeline 抽象层（基于 *llm.StreamEvent 的 chan）。它是 PR1 的"准备
// 好的死代码"：PR1 不切流量，handler.go 不调用本函数；PR2/PR3 切流量后由
// 新 handler 路径调用，确保 raw passthrough 行为与 AxonHub-half.md 第 138-142
// 行已锁定的契约一致：
//
//   - raw passthrough fan-out 是 attempt 级状态，不跨 retry 复用；
//   - retry / failover 前必须 cancel 当前 attempt、关闭 response body、
//     等待 fan-out goroutine 退出。
//
// 参数说明：
//   - parent：父 ctx；fan-out 内部派生独立 ctx，cancel 由 cleanup 触发。
//   - resp：上游响应；其 Body 会被 fan-out goroutine 持有并最终关闭。
//
// 返回值：
//   - cleanup：调用方在进入下一个 attempt 之前必须调用，幂等。它会顺序执行：
//     1) cancel 内部 ctx；
//     2) drain RawStreamCh / 内部 errChan；
//     3) 等待 goroutine done（最长 5s 超时由 cleanupRawStreamFanout 控制）；
//     4) AttemptState.Reset 清空 attempt 级字段。
//   - err：参数无效时立刻返回错误，不触发 goroutine。
//
// 调用约束：
//   - resp.Body 不可为 nil；
//   - state 不可为 nil；
//   - 同一个 state 上未清理前不可重复 Bind（会 panic 或泄露 goroutine）。
func BindRawStreamFanout(parent context.Context, resp *http.Response, state *pipeline.AttemptState) (cleanup func(), err error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("BindRawStreamFanout: response body must not be nil")
	}
	if state == nil {
		return nil, fmt.Errorf("BindRawStreamFanout: attempt state must not be nil")
	}
	if state.RawStreamCh != nil || state.RawStreamCancel != nil {
		return nil, fmt.Errorf("BindRawStreamFanout: attempt state already bound (RawStreamCh != nil); call cleanup before re-binding")
	}

	ctx, cancel := context.WithCancel(parent)
	rawCh, rawErrCh, done := startRawStreamFanout(ctx, resp.Body)

	// llmCh 是对外暴露给 pipeline.AttemptState.RawStreamCh 的事件通道。
	// 翻译 goroutine 把每个 rawStreamEvent.Bytes 拷贝为 llm.StreamEvent.Data。
	// 当上游 fan-out goroutine 关闭 rawCh 时，翻译 goroutine 会同步关闭 llmCh，
	// 让消费端正常感知流结束。
	llmCh := make(chan *llm.StreamEvent, rawStreamEventChannelSize)
	var sharedErr error

	go func() {
		defer close(llmCh)
		for {
			select {
			case <-ctx.Done():
				// 即便 ctx 取消，也尝试 best-effort 把已就绪的错误捕获到
				// sharedErr，避免 cleanup 后调用方拿不到错误。
				select {
				case fanErr, has := <-rawErrCh:
					if has {
						sharedErr = fanErr
					}
				default:
				}
				return
			case ev, ok := <-rawCh:
				if !ok {
					// 上游 fan-out goroutine 已结束（rawCh 关闭）。
					// 注意 startRawStreamFanout 内部 defers 是 LIFO 顺序，
					// errChan 实际比 eventChan 更早关闭；这里在 rawCh 关闭后
					// 用非阻塞 receive 收集错误，避免在 select 中同时观察两
					// 个 channel 时因 errChan 先关闭而提前返回，丢失 rawCh
					// 缓冲区里尚未消费的 event。
					select {
					case fanErr, has := <-rawErrCh:
						if has {
							sharedErr = fanErr
						}
					default:
					}
					return
				}
				// 拷贝 Bytes 防止与 fan-out goroutine 复用底层切片。
				data := append([]byte(nil), ev.Bytes...)
				select {
				case <-ctx.Done():
					return
				case llmCh <- &llm.StreamEvent{Data: data}:
				}
			}
		}
	}()

	// 把 attempt 级状态绑到 state 上。
	state.RawProviderResponse = resp
	state.RawStreamCh = llmCh
	state.RawStreamCancel = cancel
	state.RawStreamErrRef = &sharedErr

	cleanupOnce := false
	cleanup = func() {
		if cleanupOnce {
			return
		}
		cleanupOnce = true
		// 1) 取消 ctx → 触发 fan-out goroutine 与翻译 goroutine 退出。
		cleanupRawStreamFanout(cancel, rawCh, rawErrCh, done)
		// 2) 清空 state attempt 级字段，回到"未绑定"状态以便下一次 attempt 复用。
		state.Reset()
	}
	return cleanup, nil
}

// CollectFanoutErr 在 pipeline 完成 raw passthrough 后读取 fan-out goroutine
// 写入的终止错误（若有）。
//
// 用法：
//
//	cleanup, err := common.BindRawStreamFanout(ctx, resp, state)
//	if err != nil { ... }
//	defer cleanup()
//	// 消费完 state.RawStreamCh 后：
//	if streamErr := common.CollectFanoutErr(state); streamErr != nil { ... }
//
// 当 RawStreamErrRef 为 nil 时返回 nil；当 *RawStreamErrRef 为 nil 时同样返回 nil。
func CollectFanoutErr(state *pipeline.AttemptState) error {
	if state == nil || state.RawStreamErrRef == nil {
		return nil
	}
	if errors.Is(*state.RawStreamErrRef, nil) || *state.RawStreamErrRef == nil {
		return nil
	}
	return *state.RawStreamErrRef
}
