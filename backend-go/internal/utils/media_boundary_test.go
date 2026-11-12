package utils

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesToolResultMediaBudget(t *testing.T) {
	audio := base64.StdEncoding.EncodeToString(makeWAV(16000, 1, 16, 32000*10, 64))
	for _, kind := range []string{"function_call_output", "custom_tool_call_output"} {
		body, _ := json.Marshal(map[string]interface{}{"input": []interface{}{map[string]interface{}{"type": kind, "call_id": "c1", "output": []interface{}{map[string]interface{}{"type": "input_audio", "input_audio": map[string]string{"data": audio, "format": "wav"}}}}}})
		if got := EstimateResponsesRequestTokens(body); got < 320 || got > 400 {
			t.Fatalf("%s media budget=%d, want about 320", kind, got)
		}
	}
}

func TestNestedClaudeToolResultMediaBudget(t *testing.T) {
	audio := base64.StdEncoding.EncodeToString(makeWAV(16000, 1, 16, 32000*10, 64))
	body, _ := json.Marshal(map[string]interface{}{"messages": []interface{}{map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "tool_result", "tool_use_id": "t1", "content": []interface{}{map[string]interface{}{"type": "input_audio", "input_audio": map[string]string{"data": audio, "format": "wav"}}}}}}}})
	if got := EstimateRequestTokens(body); got < 320 || got > 450 {
		t.Fatalf("nested tool result media budget=%d", got)
	}
}

func TestPCMRateBudget(t *testing.T) {
	for _, mime := range []string{"audio/pcm;rate=16000", "audio/L16;rate=16000;channels=1"} {
		b64 := base64.StdEncoding.EncodeToString(make([]byte, 32000*10))
		if got := estimateAudioTokensFromBase64(b64, mime, ""); got != 320 {
			t.Fatalf("%s budget=%d, want 320", mime, got)
		}
	}
}

func TestTextAttachmentUsesContentBudget(t *testing.T) {
	for _, text := range []string{"hello", strings.Repeat("x", 40000)} {
		b64 := base64.StdEncoding.EncodeToString([]byte(text))
		body, _ := json.Marshal(map[string]interface{}{"input": []interface{}{map[string]interface{}{"type": "message", "role": "user", "content": []interface{}{map[string]interface{}{"type": "input_file", "file_data": "data:text/plain;base64," + b64}}}}})
		if got, want := EstimateResponsesRequestTokens(body), estimateResponsesTextTokens(text); got != want {
			t.Fatalf("text file budget=%d, want %d", got, want)
		}
	}
}
