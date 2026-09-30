package common

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

// ChunkChannelReadCloser exposes chunks read by a background goroutine as an io.ReadCloser.
// Preflight code can consume some chunks from the same channel first, then wrap the remaining
// channel with this reader so no bytes are lost between preflight and normal streaming.
type ChunkChannelReadCloser struct {
	chunks  <-chan []byte
	errs    <-chan error
	closer  io.Closer
	current []byte
}

func NewChunkChannelReadCloser(chunks <-chan []byte, errs <-chan error, closer io.Closer) *ChunkChannelReadCloser {
	return &ChunkChannelReadCloser{chunks: chunks, errs: errs, closer: closer}
}

func (r *ChunkChannelReadCloser) Read(p []byte) (int, error) {
	for len(r.current) == 0 {
		chunk, ok := <-r.chunks
		if !ok {
			select {
			case err := <-r.errs:
				if err != nil {
					return 0, err
				}
			default:
			}
			return 0, io.EOF
		}
		r.current = chunk
	}

	n := copy(p, r.current)
	r.current = r.current[n:]
	return n, nil
}

func (r *ChunkChannelReadCloser) ReadWithTimeout(p []byte, timeout time.Duration) (int, error, bool) {
	if timeout <= 0 {
		n, err := r.Read(p)
		return n, err, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for len(r.current) == 0 {
		select {
		case chunk, ok := <-r.chunks:
			if !ok {
				select {
				case err := <-r.errs:
					if err != nil {
						return 0, err, false
					}
				default:
				}
				return 0, io.EOF, false
			}
			r.current = chunk
		case <-timer.C:
			return 0, nil, true
		}
	}
	n := copy(p, r.current)
	r.current = r.current[n:]
	return n, nil, false
}

func (r *ChunkChannelReadCloser) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

type prefixedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func NewPrefixedReadCloser(prefix []byte, body io.ReadCloser) io.ReadCloser {
	return &prefixedReadCloser{
		reader: io.MultiReader(bytes.NewReader(prefix), body),
		closer: body,
	}
}

func (r *prefixedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *prefixedReadCloser) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// RequestDone 返回上游请求上下文的 Done 通道。
// provider 一律用 c.Request.Context() 构造上游请求，因此 handler 返回（含预检失败后
// 走 failover）或客户端断开时该通道会关闭——正好是"消费者不会再读 chunkChan"的信号。
// resp / resp.Request 为空时返回 nil，调用方按不启用早退收敛处理。
func RequestDone(resp *http.Response) <-chan struct{} {
	if resp == nil || resp.Request == nil {
		return nil
	}
	return resp.Request.Context().Done()
}

// StartBodyChunkReader 启动 body → chunk 通道的读取 goroutine。
//
// done 关闭后读取 goroutine 立即退出：预检测在检出流内错误、首字超时或断流时会提前
// 返回，此时只 close body 并不能唤醒阻塞在 `chunkChan <- chunk` 上的发送（缓冲写满后
// 就是永久阻塞），每次早退都会泄漏一个 goroutine 与最多 bufferSize×chunkSize 字节。
// 传 nil 表示不启用（保持旧行为，供不方便拿到上下文的调用方使用）。
//
// 注意：done 是"整个请求"的生命周期，不是"本次预检"的生命周期。预检失败后转下一个
// 渠道时 goroutine 会继续存活到本次请求结束（handler 返回时收敛），不会跨请求泄漏。
func StartBodyChunkReader(body io.ReadCloser, chunkSize int, bufferSize int, done <-chan struct{}) (<-chan []byte, <-chan error) {
	if chunkSize <= 0 {
		chunkSize = 32 * 1024
	}
	if bufferSize <= 0 {
		bufferSize = 16
	}

	chunkChan := make(chan []byte, bufferSize)
	errChan := make(chan error, 1)
	go func() {
		defer close(chunkChan)
		buf := make([]byte, chunkSize)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				// done 为 nil 时该 case 永不就绪，等价于阻塞发送。
				select {
				case chunkChan <- chunk:
				case <-done:
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					select {
					case errChan <- err:
					case <-done:
					}
				}
				return
			}
		}
	}()
	return chunkChan, errChan
}
