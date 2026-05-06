// stream_verify - 对比验证流式响应的 Token 统计和事件格式
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type UsageInfo struct {
	InputTokens              interface{} `json:"input_tokens"`
	OutputTokens             interface{} `json:"output_tokens"`
	CacheCreationInputTokens interface{} `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     interface{} `json:"cache_read_input_tokens,omitempty"`
}

type EventInfo struct {
	Type    string                 `json:"type"`
	Message map[string]interface{} `json:"message,omitempty"`
	Usage   *UsageInfo             `json:"usage,omitempty"`
	Delta   map[string]interface{} `json:"delta,omitempty"`
	Index   int                    `json:"index,omitempty"`
}

type VerifyResult struct {
	Name            string
	EventTypes      []string
	UsageEvents     []UsageInfo
	FinalUsage      *UsageInfo
	ContentLength   int
	EventCount      int
	HasMessageStart bool
	HasMessageStop  bool
	HasContentBlock bool
	Errors          []string
	FirstByteMs     int64
	TotalMs         int64
	RawContent      string
}

func main() {
	proxyURL := flag.String("proxy", "http://localhost:3000", "代理服务器地址")
	upstreamURL := flag.String("upstream", "", "上游服务器地址（用于对比）")
	proxyKey := flag.String("proxy-key", "", "代理 API Key")
	upstreamKey := flag.String("upstream-key", "", "上游 API Key")
	model := flag.String("model", "claude-opus-4-5-20251101", "模型名称")
	prompt := flag.String("prompt", "说一个简短的笑话", "测试 prompt")
	verbose := flag.Bool("v", false, "显示详细事件")
	flag.Parse()

	if *proxyKey == "" {
		*proxyKey = os.Getenv("PROXY_ACCESS_KEY")
	}
	if *upstreamKey == "" {
		*upstreamKey = os.Getenv("UPSTREAM_API_KEY")
	}

	if *proxyKey == "" {
		fmt.Println("错误: 需要 -proxy-key 参数或 PROXY_ACCESS_KEY 环境变量")
		os.Exit(1)
	}

	// 测试代理
	fmt.Println("========== 测试代理服务器 ==========")
	proxyResult := verifyStream("代理", *proxyURL, *proxyKey, *model, *prompt, *verbose)
	printResult(proxyResult)

	// 测试上游（如果提供）
	var upstreamResult *VerifyResult
	if *upstreamURL != "" && *upstreamKey != "" {
		fmt.Println("\n========== 测试上游服务器 ==========")
		upstreamResult = verifyStream("上游", *upstreamURL, *upstreamKey, *model, *prompt, *verbose)
		printResult(upstreamResult)

		// 对比分析
		fmt.Println("\n========== 对比分析 ==========")
		compareResults(proxyResult, upstreamResult)
	}
}

func verifyStream(name, baseURL, apiKey, model, prompt string, verbose bool) *VerifyResult {
	result := &VerifyResult{Name: name}
	startTime := time.Now()

	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 500,
		"stream":     true,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14")
	req.Header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	req.Header.Set("User-Agent", "claude-cli/2.0.74 (external, cli)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-Package-Version", "0.70.0")
	req.Header.Set("X-Stainless-Runtime", "node")
	req.Header.Set("X-Stainless-Runtime-Version", "v24.3.0")
	req.Header.Set("X-Stainless-Helper-Method", "stream")
	req.Header.Set("X-Stainless-Retry-Count", "0")
	req.Header.Set("X-Stainless-Timeout", "200")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("请求失败: %v", err))
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		result.Errors = append(result.Errors, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)))
		return result
	}

	var firstByte bool
	var contentBuf strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !firstByte {
			result.FirstByteMs = time.Since(startTime).Milliseconds()
			firstByte = true
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}

		result.EventCount++
		var event EventInfo
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("JSON解析失败: %s", jsonStr[:min(100, len(jsonStr))]))
			continue
		}

		result.EventTypes = append(result.EventTypes, event.Type)

		if verbose {
			fmt.Printf("[%s][%s] %s\n", name, event.Type, jsonStr)
		}

		switch event.Type {
		case "message_start":
			result.HasMessageStart = true
			if event.Message != nil {
				if usage, ok := event.Message["usage"].(map[string]interface{}); ok {
					u := extractUsage(usage)
					result.UsageEvents = append(result.UsageEvents, u)
				}
			}
		case "message_stop":
			result.HasMessageStop = true
		case "content_block_start":
			result.HasContentBlock = true
		case "content_block_delta":
			if event.Delta != nil {
				if text, ok := event.Delta["text"].(string); ok {
					contentBuf.WriteString(text)
				}
			}
		case "message_delta":
			if event.Usage != nil {
				result.UsageEvents = append(result.UsageEvents, *event.Usage)
				result.FinalUsage = event.Usage
			}
		}

		// 检查顶层 usage
		if event.Usage != nil && event.Type != "message_delta" {
			result.UsageEvents = append(result.UsageEvents, *event.Usage)
		}
	}

	result.TotalMs = time.Since(startTime).Milliseconds()
	result.ContentLength = contentBuf.Len()
	result.RawContent = contentBuf.String()

	// 验证检查
	if !result.HasMessageStart {
		result.Errors = append(result.Errors, "缺少 message_start 事件")
	}
	if !result.HasMessageStop {
		result.Errors = append(result.Errors, "缺少 message_stop 事件")
	}
	if result.FinalUsage == nil {
		result.Errors = append(result.Errors, "缺少最终 usage 数据")
	} else {
		if result.FinalUsage.InputTokens == nil {
			result.Errors = append(result.Errors, "input_tokens 为 nil")
		} else if toInt(result.FinalUsage.InputTokens) <= 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("input_tokens 异常: %v", result.FinalUsage.InputTokens))
		}
		if result.FinalUsage.OutputTokens == nil {
			result.Errors = append(result.Errors, "output_tokens 为 nil")
		} else if toInt(result.FinalUsage.OutputTokens) <= 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("output_tokens 异常: %v", result.FinalUsage.OutputTokens))
		}
	}
	if result.ContentLength == 0 {
		result.Errors = append(result.Errors, "响应内容为空")
	}

	return result
}

func extractUsage(m map[string]interface{}) UsageInfo {
	return UsageInfo{
		InputTokens:              m["input_tokens"],
		OutputTokens:             m["output_tokens"],
		CacheCreationInputTokens: m["cache_creation_input_tokens"],
		CacheReadInputTokens:     m["cache_read_input_tokens"],
	}
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func printResult(r *VerifyResult) {
	fmt.Printf("\n--- %s ---\n", r.Name)
	fmt.Printf("事件总数: %d\n", r.EventCount)
	fmt.Printf("首字节延迟: %dms\n", r.FirstByteMs)
	fmt.Printf("总耗时: %dms\n", r.TotalMs)
	fmt.Printf("响应内容长度: %d 字符\n", r.ContentLength)

	fmt.Println("\n事件完整性:")
	fmt.Printf("  message_start: %v\n", r.HasMessageStart)
	fmt.Printf("  content_block: %v\n", r.HasContentBlock)
	fmt.Printf("  message_stop: %v\n", r.HasMessageStop)

	fmt.Println("\nUsage 统计:")
	if r.FinalUsage != nil {
		fmt.Printf("  input_tokens: %v\n", r.FinalUsage.InputTokens)
		fmt.Printf("  output_tokens: %v\n", r.FinalUsage.OutputTokens)
		if r.FinalUsage.CacheCreationInputTokens != nil {
			fmt.Printf("  cache_creation: %v\n", r.FinalUsage.CacheCreationInputTokens)
		}
		if r.FinalUsage.CacheReadInputTokens != nil {
			fmt.Printf("  cache_read: %v\n", r.FinalUsage.CacheReadInputTokens)
		}
	} else {
		fmt.Println("  无 usage 数据!")
	}

	fmt.Println("\n事件类型统计:")
	typeCount := make(map[string]int)
	for _, t := range r.EventTypes {
		typeCount[t]++
	}
	for t, c := range typeCount {
		fmt.Printf("  %s: %d\n", t, c)
	}

	if len(r.Errors) > 0 {
		fmt.Println("\n❌ 发现问题:")
		for _, e := range r.Errors {
			fmt.Printf("  • %s\n", e)
		}
	} else {
		fmt.Println("\n✅ 验证通过")
	}
}

func compareResults(proxy, upstream *VerifyResult) {
	fmt.Println("\n--- 性能对比 ---")
	fmt.Printf("首字节延迟: 代理 %dms vs 上游 %dms (差值: %+dms)\n",
		proxy.FirstByteMs, upstream.FirstByteMs, proxy.FirstByteMs-upstream.FirstByteMs)
	fmt.Printf("总耗时: 代理 %dms vs 上游 %dms (差值: %+dms)\n",
		proxy.TotalMs, upstream.TotalMs, proxy.TotalMs-upstream.TotalMs)

	fmt.Println("\n--- Token 统计对比 ---")
	if proxy.FinalUsage != nil && upstream.FinalUsage != nil {
		proxyInput := toInt(proxy.FinalUsage.InputTokens)
		upstreamInput := toInt(upstream.FinalUsage.InputTokens)
		proxyOutput := toInt(proxy.FinalUsage.OutputTokens)
		upstreamOutput := toInt(upstream.FinalUsage.OutputTokens)

		fmt.Printf("input_tokens: 代理 %d vs 上游 %d", proxyInput, upstreamInput)
		if proxyInput != upstreamInput {
			fmt.Printf(" ⚠️ 不一致 (差值: %+d)\n", proxyInput-upstreamInput)
		} else {
			fmt.Println(" ✅")
		}

		fmt.Printf("output_tokens: 代理 %d vs 上游 %d", proxyOutput, upstreamOutput)
		if proxyOutput != upstreamOutput {
			fmt.Printf(" ⚠️ 不一致 (差值: %+d)\n", proxyOutput-upstreamOutput)
		} else {
			fmt.Println(" ✅")
		}
	} else {
		if proxy.FinalUsage == nil {
			fmt.Println("⚠️ 代理缺少 usage 数据")
		}
		if upstream.FinalUsage == nil {
			fmt.Println("⚠️ 上游缺少 usage 数据")
		}
	}

	fmt.Println("\n--- 内容对比 ---")
	fmt.Printf("内容长度: 代理 %d vs 上游 %d", proxy.ContentLength, upstream.ContentLength)
	if proxy.ContentLength != upstream.ContentLength {
		fmt.Printf(" ⚠️ 不一致 (差值: %+d)\n", proxy.ContentLength-upstream.ContentLength)
	} else {
		fmt.Println(" ✅")
	}

	if proxy.RawContent == upstream.RawContent {
		fmt.Println("内容完全一致 ✅")
	} else {
		fmt.Println("内容不一致 ⚠️")
		fmt.Println("\n代理响应内容:")
		fmt.Println(proxy.RawContent)
		fmt.Println("\n上游响应内容:")
		fmt.Println(upstream.RawContent)
	}

	fmt.Println("\n--- 事件序列对比 ---")
	proxyTypes := strings.Join(proxy.EventTypes, " → ")
	upstreamTypes := strings.Join(upstream.EventTypes, " → ")
	if proxyTypes == upstreamTypes {
		fmt.Println("事件序列一致 ✅")
	} else {
		fmt.Println("事件序列不一致 ⚠️")
		fmt.Printf("代理: %s\n", proxyTypes)
		fmt.Printf("上游: %s\n", upstreamTypes)
	}

	fmt.Println("\n--- 问题汇总 ---")
	allGood := len(proxy.Errors) == 0 && len(upstream.Errors) == 0 &&
		proxy.FinalUsage != nil && upstream.FinalUsage != nil &&
		toInt(proxy.FinalUsage.InputTokens) == toInt(upstream.FinalUsage.InputTokens) &&
		toInt(proxy.FinalUsage.OutputTokens) == toInt(upstream.FinalUsage.OutputTokens) &&
		proxy.RawContent == upstream.RawContent

	if allGood {
		fmt.Println("✅ 代理与上游完全一致，无问题")
	} else {
		if len(proxy.Errors) > 0 {
			fmt.Println("\n代理问题:")
			for _, e := range proxy.Errors {
				fmt.Printf("  • %s\n", e)
			}
		}
		if len(upstream.Errors) > 0 {
			fmt.Println("\n上游问题:")
			for _, e := range upstream.Errors {
				fmt.Printf("  • %s\n", e)
			}
		}
	}
}
