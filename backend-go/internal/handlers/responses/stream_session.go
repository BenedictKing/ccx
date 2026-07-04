package responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
)

// streamOutputCollector 从 Responses SSE 流中收集 output items 和 responseID，
// 用于流式路径的 session 回写。reasoning items 的 encrypted_content 会被完整保留，
// 使"推理状态"在流式路径下也能进入 Session.Messages 而非被转发即丢弃。
type streamOutputCollector struct {
	items      []types.ResponsesItem
	responseID string
}

func newStreamOutputCollector() *streamOutputCollector {
	return &streamOutputCollector{items: make([]types.ResponsesItem, 0, 8)}
}

// processEvent 解析单个（转换后的）Responses SSE 事件，累积 output items。
// 优先从 response.output_item.done 增量收集（保留原始 item 结构与 encrypted_content），
// 并在 response.completed/response.incomplete 事件中兜底提取完整 output 数组及 response id。
func (col *streamOutputCollector) processEvent(event string) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEDataLine(line)
		if !ok {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		eventType, _ := data["type"].(string)
		switch eventType {
		case "response.output_item.done":
			if itemMap, ok := data["item"].(map[string]interface{}); ok {
				col.items = append(col.items, types.NormalizeResponsesItem(types.ResponsesItemFromMap(itemMap)))
			}
		case "response.completed", "response.incomplete":
			// responseID 可能在顶层 data["id"]，或嵌套在 data["response"]["id"]（response.completed 格式）
			if id, ok := data["id"].(string); ok && id != "" && col.responseID == "" {
				col.responseID = id
			}
			if respObj, ok := data["response"].(map[string]interface{}); ok {
				if id, ok := respObj["id"].(string); ok && id != "" && col.responseID == "" {
					col.responseID = id
				}
			}
			// 兜底：若未通过 output_item.done 收集到 items，从终止事件的 output 数组提取
			if len(col.items) == 0 {
				// output 可能在顶层 data["output"] 或嵌套在 data["response"]["output"]
				var outputArr []interface{}
				if arr, ok := data["output"].([]interface{}); ok {
					outputArr = arr
				} else if respObj, ok := data["response"].(map[string]interface{}); ok {
					if arr, ok := respObj["output"].([]interface{}); ok {
						outputArr = arr
					}
				}
				for _, rawItem := range outputArr {
					if itemMap, ok := rawItem.(map[string]interface{}); ok {
						col.items = append(col.items, types.NormalizeResponsesItem(types.ResponsesItemFromMap(itemMap)))
					}
				}
			}
		}
	}
}

// extractSSEDataLine 从单行中提取 "data:" 后的 JSON 负载。
func extractSSEDataLine(line string) (string, bool) {
	if strings.HasPrefix(line, "data:") {
		s := strings.TrimPrefix(line, "data:")
		return strings.TrimPrefix(s, " "), true
	}
	return "", false
}

// writeStreamSession 将流式收集到的 input/output items 回写到 session。
// 即使客户端中途断连也应执行，以保证会话历史（含 reasoning encrypted_content）完整。
// 与非流式路径 response.go:handleSuccess 的回写逻辑对齐，但不修改任何客户端可见输出。
// 返回写入的 sessionID（空字符串表示未创建/未写入 session）。
func writeStreamSession(
	sessionManager *session.SessionManager,
	originalReq *types.ResponsesRequest,
	collector *streamOutputCollector,
) string {
	if sessionManager == nil || originalReq == nil {
		return ""
	}
	// 尊重 store 语义，与非流式路径一致
	if originalReq.Store != nil && !*originalReq.Store {
		return ""
	}

	sess, err := sessionManager.GetOrCreateSession(originalReq.PreviousResponseID)
	if err != nil {
		// previousResponseID 无效时不创建新会话，与非流式路径行为一致
		return ""
	}

	inputItems, _ := parseInputToItems(originalReq.Input)
	for _, item := range inputItems {
		sessionManager.AppendMessage(sess.ID, item, 0)
	}

	for _, item := range collector.items {
		sessionManager.AppendMessage(sess.ID, item, 0)
	}

	responseID := collector.responseID
	if responseID == "" {
		responseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}

	sessionManager.UpdateLastResponseID(sess.ID, responseID)
	sessionManager.RecordResponseMapping(responseID, sess.ID)
	return sess.ID
}
