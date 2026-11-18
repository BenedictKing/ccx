package providers

// Responses 的内容过滤不能误报为长度上限，否则客户端可能错误压缩或续推理。
func responsesIncompleteStopReason(response map[string]interface{}) string {
	details, _ := response["incomplete_details"].(map[string]interface{})
	if details["reason"] == "content_filter" {
		return "refusal"
	}
	return "max_tokens"
}
