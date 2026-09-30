package common

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// done 关闭后读取 goroutine 必须退出：预检测在检出流内错误/首字超时/断流时会提前返回，
// 此时只 close body 唤不醒阻塞在 `chunkChan <- chunk` 上的发送，旧实现会永久泄漏一个
// goroutine 与最多 bufferSize×chunkSize 字节。
func TestStartBodyChunkReaderStopsWhenDoneClosed(t *testing.T) {
	// 体量远大于缓冲，确保消费者不读时 goroutine 一定会卡在发送上
	body := io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20)))
	done := make(chan struct{})
	close(done) // 模拟"消费者已经离开"

	chunks, _ := StartBodyChunkReader(body, 1024, 1, done)

	// 只允许读走极少量 chunk：若 goroutine 仍在按 body 推进，说明 done 没生效
	// （旧实现需要排空整个 1MB 才会关闭通道）。
	deadline := time.After(2 * time.Second)
	for i := 0; i < 8; i++ {
		select {
		case _, ok := <-chunks:
			if !ok {
				return // 通道已关闭：goroutine 已退出
			}
		case <-deadline:
			t.Fatal("done 关闭后读取 goroutine 未退出（goroutine 泄漏）")
		}
	}
	t.Fatal("done 关闭后通道仍未关闭：读取 goroutine 还在推进 body")
}

// done 为 nil 时保持旧的阻塞语义：消费者正常读取时数据一字不丢。
func TestStartBodyChunkReaderNilDoneKeepsBlockingSemantics(t *testing.T) {
	payload := bytes.Repeat([]byte("abc"), 1000)
	body := io.NopCloser(bytes.NewReader(payload))

	chunks, errs := StartBodyChunkReader(body, 64, 2, nil)

	var got bytes.Buffer
	deadline := time.After(2 * time.Second)
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				if got.Len() != len(payload) {
					t.Fatalf("读到 %d 字节, want %d", got.Len(), len(payload))
				}
				if !bytes.Equal(got.Bytes(), payload) {
					t.Fatal("透传内容与输入不一致")
				}
				return
			}
			got.Write(chunk)
		case err := <-errs:
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		case <-deadline:
			t.Fatal("读取超时")
		}
	}
}

// RequestDone 从上游响应取请求上下文；缺少 resp/Request 时返回 nil（不启用早退收敛）。
func TestRequestDoneFallsBackToNil(t *testing.T) {
	if got := RequestDone(nil); got != nil {
		t.Fatalf("nil resp 应返回 nil, got %v", got)
	}
	if got := RequestDone(&http.Response{}); got != nil {
		t.Fatalf("缺少 Request 应返回 nil, got %v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp := &http.Response{Request: req}
	done := RequestDone(resp)
	if done == nil {
		t.Fatal("有上下文时必须返回 Done 通道")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消上下文后 Done 通道必须关闭")
	}
}
