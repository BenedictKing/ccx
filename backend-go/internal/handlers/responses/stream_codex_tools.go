package responses

import (
	"encoding/json"
	"strings"

	"github.com/BenedictKing/ccx/internal/converters"
)

// responsesStreamToolRemapper 仅缓冲需要还原的 function_call，正文保持实时输出。
type responsesStreamToolRemapper struct {
	ctx     *converters.CodexToolContext
	pending map[string]*responsesFoldBufferedOutput
	seq     int
}

func newResponsesStreamToolRemapper(ctx *converters.CodexToolContext) *responsesStreamToolRemapper {
	return &responsesStreamToolRemapper{ctx: ctx, pending: make(map[string]*responsesFoldBufferedOutput)}
}

func (r *responsesStreamToolRemapper) processLine(line string) []string {
	line = strings.TrimSpace(line)
	// 输出会重新构造完整 SSE 帧，不能留下被缓冲工具事件的 event: 行。
	if line == "" || strings.HasPrefix(line, "event:") {
		return nil
	}
	if !strings.HasPrefix(line, "data:") {
		return []string{line + "\n\n"}
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
		return []string{line + "\n\n"}
	}
	typ, _ := event["type"].(string)
	index := responsesFoldOutputIndexKey(event["output_index"])
	if typ == "response.output_item.added" {
		item := mapFromInterface(event["item"])
		name, _ := item["name"].(string)
		_, namespace := r.ctx.OpenAINameForFunctionTool(name)
		if item["type"] == "function_call" && (r.ctx.IsCustomToolProxy(name) || namespace != "") {
			r.pending[index] = &responsesFoldBufferedOutput{item: item}
		}
	}
	events := []map[string]interface{}{event}
	if entry := r.pending[index]; entry != nil {
		entry.events = append(entry.events, event)
		if typ != "response.output_item.done" {
			return nil
		}
		entry.item = mapFromInterface(event["item"])
		remapFoldCodexEntry(r.ctx, entry)
		events = entry.events
		delete(r.pending, index)
	}
	if isResponsesFoldTerminalType(typ) {
		response := mapFromInterface(event["response"])
		output, _ := response["output"].([]interface{})
		for i, raw := range output {
			entry := &responsesFoldBufferedOutput{item: mapFromInterface(raw)}
			remapFoldCodexEntry(r.ctx, entry)
			output[i] = entry.item
		}
	}
	result := make([]string, 0, len(events))
	for _, event := range events {
		event["sequence_number"] = r.seq
		r.seq++
		result = append(result, formatResponsesFoldSSE(event))
	}
	return result
}
