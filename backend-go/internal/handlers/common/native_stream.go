package common

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/BenedictKing/ccx/internal/utils"
)

// nativeSSEEvents 仅切分帧供预检与统计，转发内容保持原始字节。
func nativeSSEEvents(ctx context.Context, body io.Reader) (<-chan string, <-chan error) {
	events := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		scanner := bufio.NewScanner(body)
		scanner.Split(utils.SplitSSELines)
		scanner.Buffer(make([]byte, 64*1024), utils.ResponsesSSEScannerMaxBufferSize)
		var frame strings.Builder
		emit := func() bool {
			select {
			case events <- frame.String():
				frame.Reset()
				return true
			case <-ctx.Done():
				return false
			}
		}
		for scanner.Scan() {
			line := scanner.Text()
			if frame.Len()+len(line) > utils.ResponsesSSEScannerMaxBufferSize {
				errs <- fmt.Errorf("SSE frame exceeds %d bytes", utils.ResponsesSSEScannerMaxBufferSize)
				return
			}
			frame.WriteString(line)
			if strings.TrimRight(line, "\r\n") == "" && !emit() {
				return
			}
		}
		if frame.Len() > 0 && !emit() {
			return
		}
		if err := scanner.Err(); err != nil {
			errs <- err
		}
	}()
	return events, errs
}
