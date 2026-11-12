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
	// 先填满缓冲，确保生产者在消费者离开后可以退出。
	body := io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20)))
	done := make(chan struct{})
	chunks, _ := StartBodyChunkReader(body, 1024, 1, done)
	deadline := time.After(2 * time.Second)
	for len(chunks) == 0 {
		select {
		case <-deadline:
			t.Fatal("reader 未填充缓冲")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(done)
	select {
	case <-chunks: // 排空唯一的缓冲项，让关闭可见。
	case <-deadline:
		t.Fatal("取消后未能排空缓冲")
	}
	for i := 0; i < 8; i++ {
		select {
		case _, ok := <-chunks:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("done 关闭后 reader 未退出")
		}
	}
	t.Fatal("取消后 reader 仍持续读取 body")
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
