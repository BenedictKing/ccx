package thinkingcache

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// ClaudeStreamCollector reconstructs assistant content fingerprints from Claude SSE.
type ClaudeStreamCollector struct {
	thinking strings.Builder
	blocks   map[int]*streamBlock
}

type streamBlock struct {
	Index     int
	Type      string
	ID        string
	Name      string
	Input     interface{}
	Text      strings.Builder
	InputJSON strings.Builder
	Raw       map[string]interface{}
}

func NewClaudeStreamCollector() *ClaudeStreamCollector {
	return &ClaudeStreamCollector{
		blocks: make(map[int]*streamBlock),
	}
}

func (c *ClaudeStreamCollector) ProcessEvent(event string) {
	if c == nil {
		return
	}

	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		data, ok := decodeEventObject(jsonStr)
		if !ok {
			continue
		}

		eventType, _ := data["type"].(string)
		switch eventType {
		case "content_block_start":
			c.processContentBlockStart(data)
		case "content_block_delta":
			c.processContentBlockDelta(data)
		case "content_block_stop":
			c.processContentBlockStop(data)
		}
	}
}

func (c *ClaudeStreamCollector) Store(sessionID string) bool {
	if c == nil {
		return false
	}
	return StoreClaudeThinkingForContent(sessionID, c.Content(), c.Thinking())
}

func (c *ClaudeStreamCollector) Thinking() string {
	if c == nil {
		return ""
	}
	return c.thinking.String()
}

func (c *ClaudeStreamCollector) Content() []interface{} {
	if c == nil || len(c.blocks) == 0 {
		return nil
	}

	indexes := make([]int, 0, len(c.blocks))
	for index := range c.blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	content := make([]interface{}, 0, len(indexes))
	for _, index := range indexes {
		if block := c.blocks[index]; block != nil {
			if normalized, ok := block.toContentBlock(); ok {
				content = append(content, normalized)
			}
		}
	}
	return content
}

func (c *ClaudeStreamCollector) processContentBlockStart(data map[string]interface{}) {
	index, ok := intFromJSON(data["index"])
	if !ok {
		return
	}

	contentBlock, ok := data["content_block"].(map[string]interface{})
	if !ok {
		return
	}

	blockType, _ := contentBlock["type"].(string)
	block := &streamBlock{
		Index: index,
		Type:  blockType,
		Raw:   cloneMap(contentBlock),
	}
	block.ID, _ = contentBlock["id"].(string)
	block.Name, _ = contentBlock["name"].(string)
	if input, exists := contentBlock["input"]; exists {
		block.Input = normalizeJSONValue(input)
	}
	if text, _ := contentBlock["text"].(string); text != "" {
		block.Text.WriteString(text)
	}
	if (blockType == "thinking" || blockType == "redacted_thinking") && isRealString(contentBlock["thinking"]) {
		thinking, _ := contentBlock["thinking"].(string)
		c.thinking.WriteString(thinking)
	}

	c.blocks[index] = block
}

func (c *ClaudeStreamCollector) processContentBlockDelta(data map[string]interface{}) {
	index, ok := intFromJSON(data["index"])
	if !ok {
		return
	}

	delta, ok := data["delta"].(map[string]interface{})
	if !ok {
		return
	}

	block := c.block(index)
	deltaType, _ := delta["type"].(string)
	switch deltaType {
	case "text_delta":
		if text, _ := delta["text"].(string); text != "" {
			if block.Type == "" {
				block.Type = "text"
			}
			block.Text.WriteString(text)
		}
	case "input_json_delta":
		if partial, _ := delta["partial_json"].(string); partial != "" {
			block.InputJSON.WriteString(partial)
		}
	case "thinking_delta", "redacted_thinking_delta":
		if thinking, _ := delta["thinking"].(string); thinking != "" {
			c.thinking.WriteString(thinking)
		}
		if text, _ := delta["text"].(string); text != "" {
			c.thinking.WriteString(text)
		}
	}
}

func (c *ClaudeStreamCollector) processContentBlockStop(data map[string]interface{}) {
	index, ok := intFromJSON(data["index"])
	if !ok {
		return
	}
	if block := c.blocks[index]; block != nil {
		block.finalizeInput()
	}
}

func (c *ClaudeStreamCollector) block(index int) *streamBlock {
	if c.blocks == nil {
		c.blocks = make(map[int]*streamBlock)
	}
	block := c.blocks[index]
	if block == nil {
		block = &streamBlock{Index: index}
		c.blocks[index] = block
	}
	return block
}

func (b *streamBlock) toContentBlock() (interface{}, bool) {
	b.finalizeInput()

	switch b.Type {
	case "", "thinking", "redacted_thinking":
		return nil, false
	case "text":
		text := b.Text.String()
		if text == "" {
			return nil, false
		}
		return map[string]interface{}{"type": "text", "text": text}, true
	case "tool_use", "server_tool_use":
		block := map[string]interface{}{"type": b.Type}
		if b.ID != "" {
			block["id"] = b.ID
		}
		if b.Name != "" {
			block["name"] = b.Name
		}
		if b.Input != nil {
			block["input"] = b.Input
		}
		return block, true
	default:
		if len(b.Raw) == 0 {
			return nil, false
		}
		return cloneMap(b.Raw), true
	}
}

func (b *streamBlock) finalizeInput() {
	if b == nil || b.InputJSON.Len() == 0 {
		return
	}
	decoder := json.NewDecoder(strings.NewReader(b.InputJSON.String()))
	decoder.UseNumber()

	var input interface{}
	if err := decoder.Decode(&input); err == nil {
		b.Input = normalizeJSONValue(input)
	}
}

func extractSSEJSONLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if jsonStr == "" || jsonStr == "[DONE]" {
		return "", false
	}
	return jsonStr, true
}

func decodeEventObject(jsonStr string) (map[string]interface{}, bool) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(jsonStr)))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return nil, false
	}
	return data, true
}

func intFromJSON(raw interface{}) (int, bool) {
	switch typed := raw.(type) {
	case json.Number:
		value, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(value), true
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(input))
	for key, value := range input {
		cloned[key] = normalizeJSONValue(value)
	}
	return cloned
}

func isRealString(value interface{}) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}
