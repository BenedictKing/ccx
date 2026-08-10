package handlers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// capabilityProbeSchema 是从 shared/capability-probe-schema.json 编译期嵌入的单一真相源。
// 该文件在后端和前端之间共享，避免探测模型列表与基础协议的双副本硬编码。
// 修改模型列表或基础协议时请编辑仓库根目录的 shared/capability-probe-schema.json，
// 并同步 backend-go/internal/handlers/embedded/capability-probe-schema.json（可直接复制）。
//
//go:embed embedded/capability-probe-schema.json
var capabilityProbeSchemaBytes []byte

type capabilityProbeSchema struct {
	SchemaVersion             int                 `json:"schemaVersion"`
	BaseProtocols             []string            `json:"baseProtocols"`
	ProbeModels               map[string][]string `json:"probeModels"`
	FrontendPlaceholderModels map[string][]string `json:"frontendPlaceholderModels"`
}

const (
	capabilityProbeModelClaudeFable5 = "claude-fable-5"
	capabilityProbeModelClaudeOpus48 = "claude-opus-4-8"
)

var loadedCapabilityProbeSchema = loadCapabilityProbeSchema()

func loadCapabilityProbeSchema() capabilityProbeSchema {
	var schema capabilityProbeSchema
	if err := json.Unmarshal(capabilityProbeSchemaBytes, &schema); err != nil {
		panic(fmt.Sprintf("[CapabilityProbe] 内置 schema 解析失败: %v", err))
	}
	return schema
}

// capabilityProbeSchemaVersion 能力探测特征集版本。
// 当探测模型列表、prompts、参数约束或协议头发生变化时必须递增，
// 以使缓存/执行键失效并避免新旧探测特征混用。
// 当前值从 shared/capability-probe-schema.json 的 schemaVersion 字段读取。
var capabilityProbeSchemaVersion = loadedCapabilityProbeSchema.SchemaVersion

// capabilityBaseProtocols 返回能力测试支持的基础协议列表（按 schema 定义顺序）。
func capabilityBaseProtocols() []string {
	return append([]string(nil), loadedCapabilityProbeSchema.BaseProtocols...)
}

// getCapabilityProbeModels 获取协议的候选模型列表（按优先级排序）
func getCapabilityProbeModels(protocol string) ([]string, error) {
	models, ok := loadedCapabilityProbeSchema.ProbeModels[protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}

	result := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m != "" {
			result = append(result, m)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no models configured for protocol: %s", protocol)
	}

	return result, nil
}

// getCapabilityProbeModel 获取协议的首选模型（兼容旧接口）
func getCapabilityProbeModel(protocol string) (string, error) {
	models, err := getCapabilityProbeModels(protocol)
	if err != nil {
		return "", err
	}
	return models[0], nil
}
