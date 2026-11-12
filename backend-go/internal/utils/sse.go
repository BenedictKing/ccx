package utils

import "bytes"

// SplitSSELines 保留换行符和末行原始字节，供透传与只读解析共用。
func SplitSSELines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// ResponsesSSEScannerMaxBufferSize 是 Responses SSE 单行扫描上限，用于容纳图片/产物等大事件。
const ResponsesSSEScannerMaxBufferSize = 32 * 1024 * 1024
