package compression

import (
	"fmt"
	"log"

	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// CompressRequestBody 压缩请求体中的 tool_result 历史。
//
// 只压 messages 数组中 role=user 的 tool_result 类型内容块；
// 不碰 system、最后一条消息、tool_use 工具参数、响应体。
//
// 返回：压缩后的请求体、是否修改、统计信息。
// fail-open：任何异常返回原 body + Compressed=false。
func CompressRequestBody(bodyBytes []byte, plan Plan) (Result, error) {
	result := Result{
		Body:           bodyBytes,
		Compressed:     false,
		Technique:      "rtk_filter",
		FidelityPassed: false,
	}

	if !plan.Enabled {
		return result, nil
	}
	if len(bodyBytes) == 0 || !gjson.ValidBytes(bodyBytes) {
		return result, nil
	}

	// 安全兜底：panic 时 fail-open
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Compression-Engine] panic recovered, fail-open: %v", r)
			result = Result{
				Body:           bodyBytes,
				Compressed:     false,
				FallbackReason: fmt.Sprintf("panic: %v", r),
			}
		}
	}()

	// 解析 messages 数组
	msgs := gjson.GetBytes(bodyBytes, "messages")
	if !msgs.IsArray() {
		return result, nil
	}
	msgArr := msgs.Array()
	if len(msgArr) <= 1 {
		// 只有一条消息（无历史），不需要压缩
		return result, nil
	}

	// 只处理除最后一条消息之外的历史消息
	// 最后一条消息（通常是最新 user 输入）不碰
	totalOriginalTokens := 0
	totalCompressedTokens := 0
	anyModified := false
	filterHitCount := 0

	// 逐消息、逐内容块处理
	// 为了避免复杂的 JSON 重建，我们用 sjson 逐字段改写
	modifiedBody := bodyBytes
	toolResultCount := 0

	for msgIdx := 0; msgIdx < len(msgArr)-1; msgIdx++ { // 跳过最后一条
		msg := msgArr[msgIdx]
		role := msg.Get("role").String()
		if role != "user" && role != "assistant" {
			continue
		}

		content := msg.Get("content")
		if !content.Exists() {
			continue
		}

		// content 可能是 string 或 array
		if content.Type == gjson.String {
			// string 内容不是 tool_result，跳过
			continue
		}
		if !content.IsArray() {
			continue
		}

		contentArr := content.Array()
		for blockIdx, block := range contentArr {
			blockType := block.Get("type").String()
			if blockType != "tool_result" {
				continue
			}

			toolResultCount++
			if toolResultCount > plan.MaxToolResults {
				// 超出条数预算，跳过
				continue
			}

			// 提取 tool_result 的内容
			contentField := block.Get("content")
			if !contentField.Exists() {
				continue
			}

			var textContent string
			var isContentString bool
			if contentField.Type == gjson.String {
				textContent = contentField.String()
				isContentString = true
			} else if contentField.IsArray() {
				// 只有单个 text 块才压缩；多个 text 块可能有独立语义，保守跳过。
				blocks := contentField.Array()
				textBlockCount := 0
				for _, b := range blocks {
					if b.Get("type").String() == "text" {
						textBlockCount++
						textContent = b.Get("text").String()
					}
				}
				if textBlockCount != 1 {
					continue
				}
			} else {
				continue
			}

			if textContent == "" {
				continue
			}

			// 超出扫描预算时跳过该结果，不能把截断副本写回原字段。
			// 截断会让保真门只验证前缀，并可能静默丢失尾部数据。
			if plan.MaxBytesPerResult > 0 && len(textContent) > plan.MaxBytesPerResult {
				continue
			}

			// 估算原始 token
			origTokens := utils.EstimateTokens(textContent)
			totalOriginalTokens += origTokens

			// 分类
			category, _, _ := ClassifyCommand(textContent, "")
			filter := GetFilter(category)

			// 按强度调整 filter 预算
			if plan.effectiveMaxLines(filter.MaxLines) != filter.MaxLines {
				filter.MaxLines = plan.effectiveMaxLines(filter.MaxLines)
			}
			if plan.effectiveMaxChars(filter.MaxChars) != filter.MaxChars {
				filter.MaxChars = plan.effectiveMaxChars(filter.MaxChars)
			}

			// 应用 filter
			filteredText, modified := ApplyFilter(textContent, filter)
			if !modified {
				totalCompressedTokens += origTokens
				continue
			}

			// 保真门校验
			fidelityResult := CheckFidelity(textContent, filteredText)
			if !fidelityResult.Passed {
				// 保真门不通过，回退原文
				totalCompressedTokens += origTokens
				continue
			}

			// 压缩后 token 估算
			compTokens := utils.EstimateTokens(filteredText)
			totalCompressedTokens += compTokens

			// 膨胀检测：单个块压完更大，回退
			if compTokens >= origTokens {
				totalCompressedTokens += origTokens - compTokens // 修正
				continue
			}

			filterHitCount++
			anyModified = true

			// 写回
			path := fmt.Sprintf("messages.%d.content.%d.content", msgIdx, blockIdx)
			if isContentString {
				var err error
				modifiedBody, err = sjson.SetBytes(modifiedBody, path, filteredText)
				if err != nil {
					log.Printf("[Compression-Engine] sjson set failed, fail-open: %v", err)
					return Result{Body: bodyBytes, Compressed: false, FallbackReason: "sjson_error"}, nil
				}
			} else {
				// 数组型 content 可能同时包含 text、image 和附件块。
				// 仅在恰好一个 text 块时改写其 text 字段，保留其它块和字段。
				blocks := contentField.Array()
				textBlockIdx := -1
				textBlockCount := 0
				for idx, b := range blocks {
					if b.Get("type").String() == "text" {
						textBlockIdx = idx
						textBlockCount++
					}
				}
				if textBlockCount != 1 {
					continue
				}

				path := fmt.Sprintf("messages.%d.content.%d.content.%d.text", msgIdx, blockIdx, textBlockIdx)
				var err error
				modifiedBody, err = sjson.SetBytes(modifiedBody, path, filteredText)
				if err != nil {
					log.Printf("[Compression-Engine] sjson set failed, fail-open: %v", err)
					return Result{Body: bodyBytes, Compressed: false, FallbackReason: "sjson_error"}, nil
				}
			}
		}
	}

	if !anyModified {
		return result, nil
	}

	// 整体膨胀检测
	if totalCompressedTokens >= totalOriginalTokens {
		return Result{
			Body:             bodyBytes,
			Compressed:       false,
			FallbackReason:   "inflation",
			OriginalTokens:   totalOriginalTokens,
			CompressedTokens: totalCompressedTokens,
		}, nil
	}

	savings := 0.0
	if totalOriginalTokens > 0 {
		savings = float64(totalOriginalTokens-totalCompressedTokens) / float64(totalOriginalTokens) * 100.0
	}

	return Result{
		Body:             modifiedBody,
		Compressed:       true,
		OriginalTokens:   totalOriginalTokens,
		CompressedTokens: totalCompressedTokens,
		SavingsPercent:   savings,
		Technique:        "rtk_filter",
		FilterCount:      filterHitCount,
		FidelityPassed:   true,
	}, nil
}
