package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/eventbus"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
)

// DiscoveryStatus 发现任务状态枚举。
type DiscoveryStatus string

const (
	DiscoveryStatusIdle    DiscoveryStatus = "idle"
	DiscoveryStatusRunning DiscoveryStatus = "running"
	DiscoveryStatusDone    DiscoveryStatus = "done"
	DiscoveryStatusFailed  DiscoveryStatus = "failed"
)

// ModelDiscoverySource 描述模型清单的事实来源。
// 它独立于 KeyEndpointProfile.Source；后者会在后续 L1 画像刷新时变成
// l1_passive，不能用来判断模型清单是否来自管控面或静态兜底。
const (
	ModelDiscoverySourceControlPlane    = "control_plane"
	ModelDiscoverySourceModelsAPI       = "models_api"
	ModelDiscoverySourceBuiltinManifest = "builtin_manifest"
	ModelDiscoverySourceBuiltinFallback = "builtin_fallback"
)

// EndpointDiscoveryResult 单个 (baseURL, key) 端点的发现结果。
type EndpointDiscoveryResult struct {
	KeyMask                  string               `json:"keyMask"`
	BaseURL                  string               `json:"baseUrl"`
	ModelsCount              int                  `json:"modelsCount"`
	Models                   []string             `json:"models,omitempty"`
	ProtocolOk               bool                 `json:"protocolOk"`
	ErrorMessage             string               `json:"errorMessage,omitempty"`
	ModelDiscoverySource     string               `json:"modelDiscoverySource,omitempty"`
	ModelDiscoveryMessage    string               `json:"modelDiscoveryMessage,omitempty"`
	ModelsDiscoveredAt       *time.Time           `json:"modelsDiscoveredAt,omitempty"`
	ProtocolModels           map[string][]string  `json:"protocolModels,omitempty"`
	ProtocolDiscoveredAt     map[string]time.Time `json:"protocolDiscoveredAt,omitempty"`
	ProtocolDiscoverySource  map[string]string    `json:"protocolDiscoverySource,omitempty"`
	ProtocolDiscoveryMessage map[string]string    `json:"protocolDiscoveryMessage,omitempty"`
	ProtocolDiscoveryError   map[string]string    `json:"protocolDiscoveryError,omitempty"`
	apiKey                   string               `json:"-"`
	credentialUID            string               `json:"-"`
	// usedClientFingerprint 标记该端点裸请求被客户端指纹风控拒绝、
	// 带 Claude Code 伪装头重试后成功；runDiscovery 据此学习渠道级标记。
	usedClientFingerprint bool `json:"-"`
	// declaredEndpointTypes 是 new-api 系上游在 /v1/models 里声明的 模型 -> 协议集合。
	// 仅在本轮发现内用于协议探测排序，不持久化：上游会少报，不能当作权威事实。
	declaredEndpointTypes map[string][]string `json:"-"`
}

// DiscoveryTask 单渠道发现任务的运行时状态。
type DiscoveryTask struct {
	ChannelUID string                    `json:"channelUid"`
	Status     DiscoveryStatus           `json:"status"`
	StartedAt  *time.Time                `json:"startedAt,omitempty"`
	FinishedAt *time.Time                `json:"finishedAt,omitempty"`
	Error      string                    `json:"error,omitempty"`
	Endpoints  []EndpointDiscoveryResult `json:"endpoints"`
	cancel     context.CancelFunc        `json:"-"`
	// previousCheckpoints 续传时从 taskStore 加载的已持久化端点 checkpoint；
	// 新触发时为 nil。runDiscovery 据此跳过已完成的端点。
	previousCheckpoints []CheckpointedEndpoint `json:"-"`
}

// AutoDiscoveryRunner 自动发现执行器。
// 内存状态机：每个渠道同时只运行一个发现任务，重复触发会被拒绝。
// 所有配置为空时零值即可用，不触发任何实际操作。
type AutoDiscoveryRunner struct {
	mu                             sync.Mutex
	tasks                          map[string]*DiscoveryTask // channelUID -> task
	store                          *ProfileStore             // nil 时不写画像，只记录结果
	hub                            *EventHub                 // nil 时不发布 discovery_completed/auto_mapping_applied 事件
	client                         *http.Client              // nil 时使用默认 client
	timeout                        time.Duration             // 单次请求超时，默认 10s
	volcengineControlPlaneEndpoint string

	// taskStore 后台 discovery 任务落盘层（nil 时保持纯内存行为，供旧测试与无 DB 场景）。
	taskStore *DiscoveryTaskStore
	// runnerCtx/runnerCancel 服务级 root context：关闭时取消未完成任务并保留 running
	// 状态与已持久化 checkpoint，下一次启动经 ResumeIncompleteDiscoveries 续传。
	runnerCtx    context.Context
	runnerCancel context.CancelFunc
	// gcStop 终止 GC 定时器。
	gcStop chan struct{}

	// Phase 3B-2：自动发现时同步写入模型画像（nil 时不写 model_profiles，不影响现有功能）
	ModelProfileStore *ModelProfileStore

	// eventBus 共享事件总线；nil 时不发布 manifest drift 等观测事件。
	eventBus *eventbus.Bus

	// capabilityProbeLedger 记录本发现周期内已探测的能力（按 CapabilityUID），
	// 同一扫描周期内跨账号 key 对同站点同分组协议/模型探测只执行一次，其余复用结论。
	// 周期边界与每次 runDiscovery/runDiscoveryLegacy 开始处 Reset 对齐。
	capabilityProbeLedger *config.CapabilityProbeLedger
}

// NewAutoDiscoveryRunner 创建发现执行器。
// store 可为 nil（仅记录内存结果，不写持久化画像）。
// hub 可为 nil（不发布 Phase 3A 画像变更事件，向后兼容旧调用点）。
func NewAutoDiscoveryRunner(store *ProfileStore, hub *EventHub) *AutoDiscoveryRunner {
	return &AutoDiscoveryRunner{
		tasks:                 make(map[string]*DiscoveryTask),
		store:                 store,
		hub:                   hub,
		timeout:               10 * time.Second,
		gcStop:                make(chan struct{}),
		capabilityProbeLedger: config.NewCapabilityProbeLedger(),
	}
}

// SetTaskStore 注入任务落盘层并启动服务级 root context 与 GC 定时器。
// 在 main.go 构造 runner 后、接受请求前调用；未调用时保持纯内存行为（向后兼容）。
func (r *AutoDiscoveryRunner) SetTaskStore(taskStore *DiscoveryTaskStore) {
	if taskStore == nil || taskStore.db == nil {
		return
	}
	r.mu.Lock()
	r.taskStore = taskStore
	r.mu.Unlock()
	r.runnerCtx, r.runnerCancel = context.WithCancel(context.Background())
	go r.gcLoop()
}

// SetEventBus 注入共享事件总线；nil 时所有事件发布自动变为空操作。
func (r *AutoDiscoveryRunner) SetEventBus(bus *eventbus.Bus) {
	r.mu.Lock()
	r.eventBus = bus
	r.mu.Unlock()
}

// gcLoop 周期性清理 done/failed 且 finished_at 超过 24h 的记录，不删 running。
func (r *AutoDiscoveryRunner) gcLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-r.gcStop:
			return
		case <-r.runnerCtx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			store := r.taskStore
			r.mu.Unlock()
			if store == nil {
				continue
			}
			if deleted, err := store.GC(24 * time.Hour); err != nil {
				log.Printf("[AutoDiscovery-GC] 清理失败: %v", err)
			} else if deleted > 0 {
				log.Printf("[AutoDiscovery-GC] 清理过期任务记录: %d 条", deleted)
			}
		}
	}
}

// Stop 取消未完成任务并停止 GC 定时器。
// 已持久化的 running 状态与 checkpoint 保留，供下次启动续传。
func (r *AutoDiscoveryRunner) Stop() {
	if r.runnerCancel != nil {
		r.runnerCancel()
	}
	if r.gcStop != nil {
		select {
		case <-r.gcStop:
		default:
			close(r.gcStop)
		}
	}
}

// parentContext 返回用于 discovery goroutine 的父 context：
// 有 root context（SetTaskStore 后）则用 root，否则退化为 Background（旧行为）。
func (r *AutoDiscoveryRunner) parentContext() context.Context {
	if r.runnerCtx != nil {
		return r.runnerCtx
	}
	return context.Background()
}

// ResumeIncompleteDiscoveries 在服务开始接受请求前恢复所有 running 任务。
// 必须在 SetTaskStore 之后、HTTP server 监听前同步调用。
//
// 对每条 running 记录：按 channel_uid 查找当前配置；找不到则标记 failed(渠道已删除)；
// 内存已有同 channelUID 的 running task 时跳过，避免重复 goroutine。
// previousCheckpoints 从持久化记录加载，runDiscovery 据此跳过已持久化端点。
func (r *AutoDiscoveryRunner) ResumeIncompleteDiscoveries(cfgManager *config.ConfigManager) {
	if r == nil {
		return
	}
	r.mu.Lock()
	store := r.taskStore
	r.mu.Unlock()
	if store == nil {
		return
	}

	running, err := store.LoadRunning()
	if err != nil {
		log.Printf("[AutoDiscovery-Resume] 加载 running 任务失败: %v", err)
		return
	}
	if len(running) == 0 {
		return
	}

	resumed := 0
	for _, rt := range running {
		channelUID := rt.ChannelUID
		r.mu.Lock()
		if existing, ok := r.tasks[channelUID]; ok && existing.Status == DiscoveryStatusRunning {
			r.mu.Unlock()
			log.Printf("[AutoDiscovery-Resume] 渠道 %s 内存已有 running 任务，跳过", channelUID)
			continue
		}
		r.mu.Unlock()

		if cfgManager == nil {
			// 无配置管理器无法恢复，保留 running 记录供下次启动。
			log.Printf("[AutoDiscovery-Resume] cfgManager 为 nil，跳过渠道 %s", channelUID)
			continue
		}
		cfg := cfgManager.GetConfig()
		channel := findChannelByUID(cfg, channelUID)
		if channel == nil {
			if err := store.Finish(channelUID, DiscoveryStatusFailed, "渠道已删除"); err != nil {
				log.Printf("[AutoDiscovery-Resume] 标记渠道 %s failed 失败: %v", channelUID, err)
			} else {
				log.Printf("[AutoDiscovery-Resume] 渠道 %s 已删除，标记 discovery failed", channelUID)
			}
			continue
		}

		ctx, cancel := context.WithCancel(r.parentContext())
		now := time.Now()
		task := &DiscoveryTask{
			ChannelUID:          channelUID,
			Status:              DiscoveryStatusRunning,
			StartedAt:           &now,
			cancel:              cancel,
			previousCheckpoints: rt.Endpoints,
		}
		r.mu.Lock()
		r.tasks[channelUID] = task
		r.mu.Unlock()
		go r.runDiscovery(ctx, task, channel, cfgManager)
		resumed++
		log.Printf("[AutoDiscovery-Resume] 恢复渠道 %s 的未完成 discovery（已持久化端点 %d 个）",
			channelUID, len(rt.Endpoints))
	}
	if resumed > 0 {
		log.Printf("[AutoDiscovery-Resume] 共恢复 %d 个未完成 discovery 任务", resumed)
	}
}

// GetTask 返回指定渠道的发现任务快照（nil 表示从未触发）。
func (r *AutoDiscoveryRunner) GetTask(channelUID string) *DiscoveryTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[channelUID]
	if task == nil {
		return nil
	}
	// 返回快照，不暴露 cancel
	snap := *task
	snap.cancel = nil
	// 深拷贝 Endpoints
	if len(task.Endpoints) > 0 {
		snap.Endpoints = make([]EndpointDiscoveryResult, len(task.Endpoints))
		copy(snap.Endpoints, task.Endpoints)
	}
	return &snap
}

// TriggerDiscovery 触发发现任务。
// 如果同渠道已有 running 任务则返回 false（拒绝重复触发）。
// 返回 true 表示已成功触发。
func (r *AutoDiscoveryRunner) TriggerDiscovery(channelUID string, channel *config.UpstreamConfig, cfgManager *config.ConfigManager) bool {
	started, _ := r.TriggerDiscoveryWithStatus(channelUID, channel, cfgManager)
	return started
}

// TriggerDiscoveryWithStatus 触发发现任务并区分拒绝原因。
// 返回 (started, err)：
//   - 同渠道已有 running 任务 → (false, nil)（409 语义，拒绝重复触发）
//   - taskStore 持久化 running 记录失败 → (false, err)（503 语义，不启动 goroutine）
//   - 成功 → (true, nil)
func (r *AutoDiscoveryRunner) TriggerDiscoveryWithStatus(channelUID string, channel *config.UpstreamConfig, cfgManager *config.ConfigManager) (bool, error) {
	r.mu.Lock()
	if existing, ok := r.tasks[channelUID]; ok && existing.Status == DiscoveryStatusRunning {
		log.Printf("[AutoDiscovery-Trigger] 渠道 %s 发现任务已在运行中，拒绝重复触发", channelUID)
		r.mu.Unlock()
		return false, nil
	}
	store := r.taskStore
	r.mu.Unlock()

	// 持久化 running 记录失败时不启动 goroutine，让 handler 返回 503。
	if store != nil {
		accountUID, channelKind := "", ""
		if cfgManager != nil {
			_, channelKind = findChannelIndexAndKind(cfgManager.GetConfig(), channelUID)
			if channel != nil {
				accountUID = channel.AccountUID
			}
		}
		if err := store.Start(channelUID, accountUID, channelKind); err != nil {
			log.Printf("[AutoDiscovery-Trigger] 渠道 %s 持久化 running 记录失败，不启动发现: %v", channelUID, err)
			return false, err
		}
	}

	r.mu.Lock()
	// 双重检查：释放锁期间可能被并发触发。
	if existing, ok := r.tasks[channelUID]; ok && existing.Status == DiscoveryStatusRunning {
		r.mu.Unlock()
		log.Printf("[AutoDiscovery-Trigger] 渠道 %s 发现任务已被并发触发，跳过", channelUID)
		return false, nil
	}
	ctx, cancel := context.WithCancel(r.parentContext())
	now := time.Now()
	task := &DiscoveryTask{
		ChannelUID: channelUID,
		Status:     DiscoveryStatusRunning,
		StartedAt:  &now,
		cancel:     cancel,
	}
	r.tasks[channelUID] = task
	r.mu.Unlock()

	go r.runDiscovery(ctx, task, channel, cfgManager)
	return true, nil
}

// runDiscovery 执行发现逻辑（在后台 goroutine 中运行）。
//
// 有 taskStore 时走端点级 checkpoint：逐端点探测 → 单端点画像写入 + 强制 Flush →
// 只有 Flush 成功才 SaveCheckpoint(profilePersisted=true)。全部端点处理完且最后一次
// Flush 成功才标记 done；全部端点失败则 failed。context 取消保留 running 与已存 checkpoint。
// 无 taskStore 时退化为旧的单次 collect → writeProfiles 全量行为（兼容现有测试）。
func (r *AutoDiscoveryRunner) runDiscovery(ctx context.Context, task *DiscoveryTask, channel *config.UpstreamConfig, cfgManager *config.ConfigManager) {
	defer func() {
		if rec := recover(); rec != nil {
			r.mu.Lock()
			task.Status = DiscoveryStatusFailed
			now := time.Now()
			task.FinishedAt = &now
			task.Error = fmt.Sprintf("panic: %v", rec)
			r.mu.Unlock()
			if r.taskStore != nil {
				_ = r.taskStore.Finish(task.ChannelUID, DiscoveryStatusFailed, fmt.Sprintf("panic: %v", rec))
			}
			log.Printf("[AutoDiscovery-Run] 渠道 %s 发现任务 panic: %v", task.ChannelUID, rec)
		}
	}()

	// 每个发现周期重置能力探测台账，保证同一 CapabilityUID 在本周期内只探测一次。
	if r.capabilityProbeLedger != nil {
		r.capabilityProbeLedger.Reset()
	}

	r.mu.Lock()
	store := r.taskStore
	r.mu.Unlock()

	if store == nil {
		r.runDiscoveryLegacy(ctx, task, channel, cfgManager)
		return
	}

	// checkpoint 路径：逐端点处理。
	endpoints := r.discoverEndpointsWithCheckpoint(ctx, task.ChannelUID, channel, cfgManager, task.previousCheckpoints)

	failedCount := 0
	for _, ep := range endpoints {
		if !ep.ProtocolOk {
			failedCount++
		}
	}

	var status DiscoveryStatus
	var taskErr string
	switch {
	case len(endpoints) == 0:
		// baseURL/Key 均未解析出可探测端点：多为凭证回填缺失或渠道未配置 Key，
		// 不能算发现成功，否则前端会把"重新发现"误报为已完成但模型清单仍为空。
		status = DiscoveryStatusFailed
		taskErr = "未找到可探测的端点（缺少 baseURL 或 API Key）"
	case failedCount == len(endpoints):
		status = DiscoveryStatusFailed
		taskErr = "所有端点均不可达"
	default:
		status = DiscoveryStatusDone
	}
	if ctx.Err() == nil && cfgManager != nil {
		// 探测完成后尝试自动写入 SupportedModels（安全守则：仅一致结果且用户未手动配置时写入）
		r.maybeAutoWriteChannelConfig(task.ChannelUID, channel, endpoints, cfgManager)
		r.maybeLearnClientFingerprint(task.ChannelUID, channel, endpoints, cfgManager)
		if err := r.maybeEnableDiscoveredProtocolRoutes(channel, endpoints, cfgManager); err != nil {
			status = DiscoveryStatusFailed
			taskErr = fmt.Sprintf("自动启用已发现协议失败: %v", err)
			log.Printf("[AutoDiscovery-RouteEnable] 渠道 %s 自动启用协议失败: %v", task.ChannelUID, err)
		}
	}

	r.mu.Lock()
	task.Endpoints = endpoints
	now := time.Now()
	task.FinishedAt = &now
	// context 取消：保留 running 状态与已持久化 checkpoint，不标记 done。
	if ctx.Err() != nil {
		status = DiscoveryStatusRunning
		task.FinishedAt = nil
	} else {
		task.Status = status
		task.Error = taskErr
	}
	r.mu.Unlock()

	if ctx.Err() == nil {
		if err := store.Finish(task.ChannelUID, status, taskErr); err != nil {
			log.Printf("[AutoDiscovery-Run] 渠道 %s 持久化终态失败: %v", task.ChannelUID, err)
		}
		r.publishDiscoveryComplete(task.ChannelUID, cfgManager, status, taskErr, endpoints, failedCount)
	}

	log.Printf("[AutoDiscovery-Run] 渠道 %s 发现完成: %d/%d 端点可达 (status=%s)",
		task.ChannelUID, len(endpoints)-failedCount, len(endpoints), status)
}

// runDiscoveryLegacy 无 taskStore 时的旧行为：collect 全部端点 → 一次性 writeProfiles。
func (r *AutoDiscoveryRunner) runDiscoveryLegacy(ctx context.Context, task *DiscoveryTask, channel *config.UpstreamConfig, cfgManager *config.ConfigManager) {
	// 无 taskStore 路径同样重置能力探测台账，保持与 checkpoint 路径周期语义一致。
	if r.capabilityProbeLedger != nil {
		r.capabilityProbeLedger.Reset()
	}
	endpoints := r.discoverEndpoints(ctx, channel, cfgManager)

	failedCount := 0
	for _, ep := range endpoints {
		if !ep.ProtocolOk {
			failedCount++
		}
	}
	var status DiscoveryStatus
	var taskErr string
	switch {
	case len(endpoints) == 0:
		status = DiscoveryStatusFailed
		taskErr = "未找到可探测的端点（缺少 baseURL 或 API Key）"
	case failedCount == len(endpoints):
		status = DiscoveryStatusFailed
		taskErr = "所有端点均不可达"
	default:
		status = DiscoveryStatusDone
	}

	r.writeProfiles(task.ChannelUID, channel, endpoints, cfgManager)

	if cfgManager != nil {
		r.maybeAutoWriteChannelConfig(task.ChannelUID, channel, endpoints, cfgManager)
		if err := r.maybeEnableDiscoveredProtocolRoutes(channel, endpoints, cfgManager); err != nil {
			status = DiscoveryStatusFailed
			taskErr = fmt.Sprintf("自动启用已发现协议失败: %v", err)
			log.Printf("[AutoDiscovery-RouteEnable] 渠道 %s 自动启用协议失败: %v", task.ChannelUID, err)
		}
	}

	r.mu.Lock()
	task.Endpoints = endpoints
	now := time.Now()
	task.FinishedAt = &now
	task.Status = status
	task.Error = taskErr
	r.mu.Unlock()
	r.publishDiscoveryComplete(task.ChannelUID, cfgManager, status, taskErr, endpoints, failedCount)

	log.Printf("[AutoDiscovery-Run] 渠道 %s 发现完成: %d/%d 端点可达",
		task.ChannelUID, len(endpoints)-failedCount, len(endpoints))
}

// publishDiscoveryComplete 发布 discovery_completed 事件（只读展示，不影响调度）。
func (r *AutoDiscoveryRunner) publishDiscoveryComplete(channelUID string, cfgManager *config.ConfigManager, status DiscoveryStatus, taskErr string, endpoints []EndpointDiscoveryResult, failedCount int) {
	if r.hub == nil {
		return
	}
	channelKind := ""
	if cfgManager != nil {
		_, channelKind = findChannelIndexAndKind(cfgManager.GetConfig(), channelUID)
	}
	summary := fmt.Sprintf("%d/%d 端点可达", len(endpoints)-failedCount, len(endpoints))
	if status == DiscoveryStatusFailed {
		summary = "发现失败: " + taskErr
	}
	now := time.Now()
	ev := ProfileChangeEvent{
		ChannelUID:  channelUID,
		ChannelKind: channelKind,
		EventType:   EventTypeDiscoveryComplete,
		Summary:     summary,
		CreatedAt:   now,
	}
	ev.EventUID = GenerateChangeEventUID(channelUID, ev.EventType, now)
	r.hub.Publish(ev)
}

// discoverEndpoints 遍历所有 (baseURL, key) 组合，调用 GET /v1/models。
func (r *AutoDiscoveryRunner) discoverEndpoints(ctx context.Context, channel *config.UpstreamConfig, managers ...*config.ConfigManager) []EndpointDiscoveryResult {
	baseURLs := channel.GetAllBaseURLs()
	keys := channel.APIKeys

	// 自动托管渠道的 APIKeys 在持久化时被脱敏，运行时从 apiKeyConfigs
	// 或 ManagedAccountCredential 获取实际密钥，确保管控面模型发现不被跳过。
	if len(keys) == 0 && channel.AutoManaged && len(managers) > 0 && managers[0] != nil {
		keys = r.resolveAutoManagedKeys(channel, managers[0])
	}

	if len(baseURLs) == 0 || len(keys) == 0 {
		return nil
	}

	client := r.client
	if client == nil {
		client = httpclient.GetManager().GetStandardClient(r.timeout, channel.InsecureSkipVerify, channel.ProxyURL)
	}

	var results []EndpointDiscoveryResult
	var cfgManager *config.ConfigManager
	if len(managers) > 0 {
		cfgManager = managers[0]
	}
	for _, key := range keys {
		keyBaseURLs := baseURLs
		if bound := channel.BoundBaseURLForKey(key); bound != "" {
			keyBaseURLs = []string{bound}
		}
		for _, baseURL := range keyBaseURLs {
			select {
			case <-ctx.Done():
				return results
			default:
			}

			var result EndpointDiscoveryResult
			if channel.ProviderID == "volcengine" {
				result = r.discoverVolcenginePlanEndpoint(ctx, client, channel, baseURL, key, cfgManager)
			} else {
				result = r.probeEndpoint(ctx, client, channel, baseURL, key)
			}
			result.apiKey = key
			result.credentialUID = r.resolveDiscoveryCredentialUID(channel, cfgManager, key)
			if result.ProtocolOk && channel.AutoManaged {
				r.discoverEndpointProtocols(ctx, channel, baseURL, key, &result)
			}
			logEndpointDiscovery(channel.ChannelUID, result)
			results = append(results, result)
		}
	}
	return results
}

// discoverEndpointsWithCheckpoint 端点级增量发现：每探测成功一个端点，
// 立即用当前真实 key 写入画像并强制 Flush，Flush 成功后才 SaveCheckpoint(profilePersisted=true)。
// 即使在画像写入与 checkpoint 之间崩溃，恢复时也只是重复一次幂等写入，
// 不会出现“checkpoint 已成功但画像不存在”。
// 仅跳过 previousResults 中 profilePersisted=true 且 endpointUID 仍匹配当前配置的结果；
// 失败端点不跳过，重启后允许重试。
func (r *AutoDiscoveryRunner) discoverEndpointsWithCheckpoint(ctx context.Context, channelUID string, channel *config.UpstreamConfig, cfgManager *config.ConfigManager, previous []CheckpointedEndpoint) []EndpointDiscoveryResult {
	baseURLs := channel.GetAllBaseURLs()
	keys := channel.APIKeys
	if len(keys) == 0 && channel.AutoManaged && cfgManager != nil {
		keys = r.resolveAutoManagedKeys(channel, cfgManager)
	}
	if len(baseURLs) == 0 || len(keys) == 0 {
		return nil
	}
	client := r.client
	if client == nil {
		client = httpclient.GetManager().GetStandardClient(r.timeout, channel.InsecureSkipVerify, channel.ProxyURL)
	}

	// 预计算已持久化且配置未变的端点 checkpoint，用于跳过重复探测。
	// 只有 profilePersisted=true 且 endpointUID 仍匹配当前配置 (baseURL+keyHash) 才跳过；
	// 失败端点不进入此 map，重启后允许重试。
	prevByUID := make(map[string]CheckpointedEndpoint, len(previous))
	for _, p := range previous {
		if !p.ProfilePersisted || p.EndpointUID == "" {
			continue
		}
		if r.endpointStillExists(channel, p, baseURLs, keys) {
			prevByUID[p.EndpointUID] = p
		}
	}

	// 准备 model profile 上下文。
	var globalModelCapabilities map[string]config.UpstreamModelCapability
	var channelKind string
	var channelID int
	if cfgManager != nil {
		cfg := cfgManager.GetConfig()
		globalModelCapabilities = cfg.UpstreamModelCapabilities
		var idx int
		idx, channelKind = findChannelIndexAndKind(cfg, channelUID)
		if idx >= 0 {
			channelID = idx
		}
	}

	var results []EndpointDiscoveryResult
	for _, key := range keys {
		keyBaseURLs := baseURLs
		if bound := channel.BoundBaseURLForKey(key); bound != "" {
			keyBaseURLs = []string{bound}
		}
		for _, baseURL := range keyBaseURLs {
			select {
			case <-ctx.Done():
				return results
			default:
			}

			keyHash := KeyHashFromAPIKey(key)
			canonicalBaseURL := utils.CanonicalBaseURL(baseURL, channel.ServiceType)
			endpointUID := GenerateEndpointUID(channelUID, canonicalBaseURL, keyHash)

			// 已持久化且配置未变：用 checkpoint 重建结果，不重新探测，避免重复请求 /models。
			if cp, ok := prevByUID[endpointUID]; ok {
				results = append(results, EndpointDiscoveryResult{
					KeyMask:                  utils.MaskAPIKey(key),
					BaseURL:                  baseURL,
					Models:                   cp.Models,
					ModelsCount:              cp.ModelsCount,
					ProtocolOk:               cp.ProtocolOk,
					ModelDiscoverySource:     cp.ModelDiscoverySource,
					ModelDiscoveryMessage:    cp.ModelDiscoveryMessage,
					ModelsDiscoveredAt:       cp.ModelsDiscoveredAt,
					ProtocolModels:           cloneProtocolModels(cp.ProtocolModels),
					ProtocolDiscoveredAt:     cloneTimeMap(cp.ProtocolDiscoveredAt),
					ProtocolDiscoverySource:  cloneStringMap(cp.ProtocolDiscoverySource),
					ProtocolDiscoveryMessage: cloneStringMap(cp.ProtocolDiscoveryMessage),
					ProtocolDiscoveryError:   cloneStringMap(cp.ProtocolDiscoveryError),
					apiKey:                   key,
					credentialUID:            cp.CredentialUID,
				})
				continue
			}

			var result EndpointDiscoveryResult
			if channel.ProviderID == "volcengine" {
				result = r.discoverVolcenginePlanEndpoint(ctx, client, channel, baseURL, key, cfgManager)
			} else {
				result = r.probeEndpoint(ctx, client, channel, baseURL, key)
			}
			result.apiKey = key
			result.credentialUID = r.resolveDiscoveryCredentialUID(channel, cfgManager, key)
			if result.ProtocolOk && channel.AutoManaged {
				r.discoverEndpointProtocols(ctx, channel, baseURL, key, &result)
			}
			logEndpointDiscovery(channel.ChannelUID, result)
			results = append(results, result)

			if !result.ProtocolOk {
				continue // 失败端点不写画像、不 checkpoint，重启后允许重试
			}

			// 先写画像并强制 Flush，成功后才写 checkpoint（至少一次 + 幂等）。
			if _, err := r.writeProfileForEndpoint(channelUID, channel, result, channelID, channelKind, globalModelCapabilities); err != nil {
				log.Printf("[AutoDiscovery-Checkpoint] 画像写入失败，不标记 checkpoint endpoint=%s: %v", endpointUID, err)
				continue
			}
			if err := r.flushStores(); err != nil {
				log.Printf("[AutoDiscovery-Checkpoint] 画像 Flush 失败，不标记 checkpoint endpoint=%s: %v", endpointUID, err)
				continue
			}
			checkpoint := CheckpointedEndpoint{
				EndpointUID:              endpointUID,
				KeyHash:                  keyHash,
				CredentialUID:            result.credentialUID,
				BaseURL:                  canonicalBaseURL,
				Models:                   append([]string(nil), result.Models...),
				ModelsCount:              result.ModelsCount,
				ProtocolOk:               result.ProtocolOk,
				Error:                    result.ErrorMessage,
				ModelDiscoverySource:     result.ModelDiscoverySource,
				ModelDiscoveryMessage:    result.ModelDiscoveryMessage,
				ModelsDiscoveredAt:       cloneTimePointer(result.ModelsDiscoveredAt),
				ProtocolModels:           cloneProtocolModels(result.ProtocolModels),
				ProtocolDiscoveredAt:     cloneTimeMap(result.ProtocolDiscoveredAt),
				ProtocolDiscoverySource:  cloneStringMap(result.ProtocolDiscoverySource),
				ProtocolDiscoveryMessage: cloneStringMap(result.ProtocolDiscoveryMessage),
				ProtocolDiscoveryError:   cloneStringMap(result.ProtocolDiscoveryError),
				ProfilePersisted:         true,
			}
			if err := r.taskStore.UpsertEndpointCheckpoint(channelUID, checkpoint); err != nil {
				log.Printf("[AutoDiscovery-Checkpoint] 写入 checkpoint 失败 endpoint=%s: %v", endpointUID, err)
			}
		}
	}
	return results
}

// endpointStillExists 判断 checkpoint 的端点身份是否仍匹配当前配置（同一 baseURL + keyHash）。
// key rotation、baseURL canonicalization 或 credential 改变后返回 false，使恢复期重试该端点。
func (r *AutoDiscoveryRunner) endpointStillExists(channel *config.UpstreamConfig, p CheckpointedEndpoint, baseURLs, keys []string) bool {
	for _, key := range keys {
		if KeyHashFromAPIKey(key) != p.KeyHash {
			continue
		}
		keyBaseURLs := baseURLs
		if bound := channel.BoundBaseURLForKey(key); bound != "" {
			keyBaseURLs = []string{bound}
		}
		for _, baseURL := range keyBaseURLs {
			canonical := utils.CanonicalBaseURL(baseURL, channel.ServiceType)
			endpointUID := GenerateEndpointUID(channel.ChannelUID, canonical, p.KeyHash)
			if endpointUID == p.EndpointUID {
				return true
			}
		}
	}
	return false
}

// resolveAutoManagedKeys 从 apiKeyConfigs 和 ManagedAccountCredential 获取自动托管渠道的实际密钥。
// 自动托管渠道的 APIKeys 在持久化时被脱敏，运行时可能为空，需要从凭证系统获取。
func (r *AutoDiscoveryRunner) resolveAutoManagedKeys(channel *config.UpstreamConfig, cfgManager *config.ConfigManager) []string {
	if cfgManager == nil || channel == nil || channel.AccountUID == "" {
		return nil
	}
	var keys []string
	seen := make(map[string]bool)
	for _, cfg := range channel.APIKeyConfigs {
		// 优先使用已回填的 Key
		if cfg.Key != "" && !seen[cfg.Key] {
			keys = append(keys, cfg.Key)
			seen[cfg.Key] = true
			continue
		}
		// 从凭证获取实际 Key
		if cfg.CredentialUID != "" {
			cred, ok := cfgManager.GetManagedAccountCredential(channel.AccountUID, cfg.CredentialUID)
			if ok && cred.APIKey != "" && !seen[cred.APIKey] {
				keys = append(keys, cred.APIKey)
				seen[cred.APIKey] = true
			}
		}
	}
	return keys
}

func (r *AutoDiscoveryRunner) resolveDiscoveryCredentialUID(channel *config.UpstreamConfig, cfgManager *config.ConfigManager, apiKey string) string {
	if channel == nil || strings.TrimSpace(apiKey) == "" {
		return ""
	}
	if credentialUID := channel.CredentialUIDForKey(apiKey); credentialUID != "" {
		return credentialUID
	}
	if cfgManager == nil || channel.AccountUID == "" {
		return ""
	}
	for _, keyConfig := range channel.APIKeyConfigs {
		if keyConfig.CredentialUID == "" {
			continue
		}
		credential, ok := cfgManager.GetManagedAccountCredential(channel.AccountUID, keyConfig.CredentialUID)
		if ok && credential.APIKey == apiKey {
			return keyConfig.CredentialUID
		}
	}
	return ""
}

func (r *AutoDiscoveryRunner) discoverVolcenginePlanEndpoint(
	ctx context.Context,
	client *http.Client,
	channel *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	cfgManager *config.ConfigManager,
) EndpointDiscoveryResult {
	result := EndpointDiscoveryResult{KeyMask: utils.MaskAPIKey(apiKey), BaseURL: baseURL, apiKey: apiKey}

	// 缺少账号上下文或未绑定 Access Key 时，回退到内置兜底模型清单，
	// 让渠道立即可用（绑定 Access Key 后会由管控面 FetchModels 覆盖为真实清单）。
	if cfgManager == nil || channel == nil || channel.AccountUID == "" {
		if r.applyVolcengineFallback(&result, channel, baseURL, "缺少自动托管账号上下文，已回退内置模型清单") {
			return result
		}
		result.ErrorMessage = "火山套餐模型发现需要自动托管账号上下文"
		return result
	}
	credentialUID := channel.CredentialUIDForKey(apiKey)
	credential, ok := cfgManager.GetManagedAccountCredential(channel.AccountUID, credentialUID)
	if !ok || credential.VolcengineAccessKey == nil {
		if r.applyVolcengineFallback(&result, channel, baseURL, "未绑定火山云 Access Key，已回退内置模型清单") {
			return result
		}
		result.ErrorMessage = "请先为该推理 Key 绑定火山云 Access Key ID 与 Secret Access Key"
		return result
	}
	planClient := &volcenginePlanClient{
		Endpoint:   r.volcengineControlPlaneEndpoint,
		HTTPClient: client,
	}
	plan, err := planClient.DetectPlan(ctx, credential.VolcengineAccessKey, volcenginePlanFromBaseURL(baseURL))
	if err != nil {
		if r.applyVolcengineFallback(&result, channel, baseURL, fmt.Sprintf("套餐识别失败(%v)，已回退内置模型清单", err)) {
			return result
		}
		result.ErrorMessage = err.Error()
		return result
	}
	models, err := planClient.FetchModels(ctx, credential.VolcengineAccessKey, plan.Plan)
	if err != nil {
		if r.applyVolcengineFallback(&result, channel, baseURL, fmt.Sprintf("模型发现失败(%v)，已回退内置模型清单", err)) {
			return result
		}
		result.ErrorMessage = err.Error()
		return result
	}
	if err := cfgManager.SetManagedAccountVolcenginePlan(channel.AccountUID, credentialUID, plan.Plan, plan.Tier, plan.Status); err != nil {
		log.Printf("[AutoDiscovery-Volcengine] 保存套餐识别结果失败 credential=%s: %v", credentialUID, err)
	}
	result.Models = models
	result.ModelsCount = len(models)
	result.ProtocolOk = true
	setModelDiscoveryMetadata(&result, ModelDiscoverySourceControlPlane, fmt.Sprintf("火山管控面 %s 模型清单", displayVolcenginePlan(plan.Plan)))
	r.publishManifestDriftIfNeeded(channel.ChannelUID, channel, baseURL, models)
	return result
}

// publishManifestDriftIfNeeded 在发现模型与内置兜底清单不一致时发布 manifest_drift 事件。
// 仅当清单来自 control_plane 且成功获取到模型时比较；事件只读展示，不影响调度。
func (r *AutoDiscoveryRunner) publishManifestDriftIfNeeded(channelUID string, channel *config.UpstreamConfig, baseURL string, discoveredModels []string) {
	if r == nil || channel == nil {
		return
	}
	r.mu.Lock()
	bus := r.eventBus
	r.mu.Unlock()
	if bus == nil {
		return
	}
	manifest, ok := lookupDiscoveryBuiltinManifest(channel, baseURL)
	if !ok || len(manifest.ModelIDs) == 0 {
		return
	}
	added, removed := diffModelLists(sortedCopy(manifest.ModelIDs), sortedCopy(discoveredModels))
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	ev := eventbus.Event{
		Type:    eventbus.TypeManifestDrift,
		Scope:   eventbus.ScopeConfig,
		Subject: channelUID,
		Payload: map[string]any{
			"added":   added,
			"removed": removed,
		},
	}
	ev.EnsureUID()
	bus.Publish(ev)
	log.Printf("[AutoDiscovery-ManifestDrift] channel=%s baseURL=%s added=%d removed=%d",
		channelUID, baseURL, len(added), len(removed))
}

// applyVolcengineFallback 在无法通过火山管控面发现模型时，尝试命中内置兜底清单。
// 命中则填充 result（ProtocolOk=true）并返回 true；未命中返回 false。
func (r *AutoDiscoveryRunner) applyVolcengineFallback(result *EndpointDiscoveryResult, channel *config.UpstreamConfig, baseURL, message string) bool {
	manifest, ok := lookupDiscoveryBuiltinManifest(channel, baseURL)
	if !ok || len(manifest.ModelIDs) == 0 {
		return false
	}
	applyBuiltinFallbackModels(result, manifest, message)
	return result.ProtocolOk
}

// probeEndpoint 探测单个 (baseURL, key) 组合。
// 优先遵循内置模型清单；否则调 GET /v1/models 检查协议可达性和模型列表。
func (r *AutoDiscoveryRunner) probeEndpoint(ctx context.Context, client *http.Client, channel *config.UpstreamConfig, baseURL, apiKey string) EndpointDiscoveryResult {
	result := EndpointDiscoveryResult{
		KeyMask: utils.MaskAPIKey(apiKey),
		BaseURL: baseURL,
		apiKey:  apiKey,
	}
	if channel == nil {
		result.ErrorMessage = "渠道配置为空"
		return result
	}

	manifest, hasManifest := lookupDiscoveryBuiltinManifest(channel, baseURL)
	if hasManifest && manifest.DisableProbe {
		if discoveryManifestServiceType(channel.ServiceType) != "messages" {
			applyBuiltinModels(&result, manifest, "内置模型清单")
			return result
		}
		verify := VerifyClaudeEndpoint(ctx, baseURL, apiKey, channel.AuthHeader)
		if verify.OK {
			applyBuiltinModels(&result, manifest, "内置模型清单")
			return result
		}
		result.ErrorMessage = verify.Message
		if result.ErrorMessage == "" && verify.Err != nil {
			result.ErrorMessage = verify.Err.Error()
		}
		if result.ErrorMessage == "" {
			result.ErrorMessage = fmt.Sprintf("HTTP %d", verify.StatusCode)
		}
		return result
	}

	// 构建 models URL。个别 provider 的协议入口与模型列表入口不同，优先使用清单覆盖值。
	modelsURL := buildModelsProbeURLForService(baseURL, channel.ServiceType)
	if manifestURL, ok := config.ResolveBuiltinModelsURL(baseURL, discoveryManifestServiceType(channel.ServiceType)); ok {
		modelsURL = manifestURL
	}

	// 设置认证头；Anthropic 风格端点首发即带 Claude Code 探针头（与 capability test 一致），
	// 其余裸发——命中客户端指纹风控时由 FetchUpstreamModels 带探针头重试，
	// 重试成功记 usedClientFingerprint，runDiscovery 据此学习渠道级标记。
	applyAuth := func(h http.Header) {
		if protocolForServiceType(channel.ServiceType) == "gemini" && !utils.HasAuthenticationHeaderOverride(channel.AuthHeader) {
			utils.SetGeminiAuthenticationHeader(h, apiKey)
		} else {
			utils.SetAuthenticationHeaderWithOverride(h, apiKey, channel.AuthHeader)
		}
	}
	useProbeHeaders := protocolForServiceType(channel.ServiceType) == "messages" || channel.LearnedClientFingerprint
	statusCode, body, learnedFingerprint, err := utils.FetchUpstreamModels(ctx, client, modelsURL, applyAuth, channel.CustomHeaders, useProbeHeaders)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("请求失败: %v", err)
		return result
	}
	result.usedClientFingerprint = learnedFingerprint

	if statusCode != http.StatusOK {
		if hasManifest && statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
			applyBuiltinFallbackModels(&result, manifest, fmt.Sprintf("models 端点返回 HTTP %d，已回退内置模型清单", statusCode))
			return result
		}
		truncated := body
		if len(truncated) > 1024 {
			truncated = truncated[:1024]
		}
		result.ErrorMessage = fmt.Sprintf("HTTP %d: %s", statusCode, string(truncated))
		return result
	}

	models := parseModelsResponse(body)
	if hasManifest {
		models = filterExcludedDiscoveryModels(models, manifest.ExcludeModelPatterns)
	}
	if len(models) == 0 && hasManifest {
		applyBuiltinFallbackModels(&result, manifest, "models 端点返回空列表，已回退内置模型清单")
		return result
	}
	result.ModelsCount = len(models)
	result.Models = models
	result.ProtocolOk = true
	result.declaredEndpointTypes = parseModelsDeclaredEndpointTypes(body)
	setModelDiscoveryMetadata(&result, ModelDiscoverySourceModelsAPI, "models API 返回实时模型清单")

	return result
}

func buildModelsProbeURL(baseURL string) string {
	return buildModelsProbeURLWithVersion(baseURL, "/v1")
}

func buildModelsProbeURLForService(baseURL, serviceType string) string {
	if protocolForServiceType(serviceType) == "gemini" {
		return buildModelsProbeURLWithVersion(baseURL, "/v1beta")
	}
	return buildModelsProbeURL(baseURL)
}

func buildModelsProbeURLWithVersion(baseURL, versionPrefix string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if verifyVersionPattern.MatchString(baseURL) || skipVersionPrefix {
		return baseURL + "/models"
	}
	return baseURL + versionPrefix + "/models"
}

func lookupDiscoveryBuiltinManifest(channel *config.UpstreamConfig, baseURL string) (config.BuiltinModelsManifest, bool) {
	if channel == nil {
		return config.BuiltinModelsManifest{}, false
	}
	serviceType := discoveryManifestServiceType(channel.ServiceType)
	if serviceType == "" {
		return config.BuiltinModelsManifest{}, false
	}
	if manifest, ok := config.LookupBuiltinManifest(baseURL, serviceType); ok {
		return manifest, true
	}
	// OpenAI 兼容渠道在运行时会把末尾 /v1 规范化掉，但清单按实际 models
	// 端点记录为 host/v1。仅在直接匹配失败时补 /v1 重试。
	if serviceType == "openai" {
		return config.LookupBuiltinManifest(strings.TrimRight(baseURL, "/")+"/v1", serviceType)
	}
	return config.BuiltinModelsManifest{}, false
}

func discoveryManifestServiceType(serviceType string) string {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude":
		return "messages"
	default:
		return strings.ToLower(strings.TrimSpace(serviceType))
	}
}

func applyBuiltinModels(result *EndpointDiscoveryResult, manifest config.BuiltinModelsManifest, message string) {
	result.Models = filterExcludedDiscoveryModels(append([]string(nil), manifest.ModelIDs...), manifest.ExcludeModelPatterns)
	result.ModelsCount = len(result.Models)
	result.ProtocolOk = len(result.Models) > 0
	result.ErrorMessage = message
	setModelDiscoveryMetadata(result, ModelDiscoverySourceBuiltinManifest, message)
}

// applyBuiltinFallbackModels 与明确配置的内置清单区分开来，记录这是一次
// 管控面/models API 失败后的回退，而不是正常的静态清单来源。
func applyBuiltinFallbackModels(result *EndpointDiscoveryResult, manifest config.BuiltinModelsManifest, message string) {
	applyBuiltinModels(result, manifest, message)
	result.ModelDiscoverySource = ModelDiscoverySourceBuiltinFallback
}

func setModelDiscoveryMetadata(result *EndpointDiscoveryResult, source, message string) {
	if result == nil {
		return
	}
	now := time.Now().UTC()
	result.ModelDiscoverySource = source
	result.ModelDiscoveryMessage = strings.TrimSpace(message)
	result.ModelsDiscoveredAt = &now
}

func logEndpointDiscovery(channelUID string, result EndpointDiscoveryResult) {
	source := result.ModelDiscoverySource
	if source == "" {
		source = "unknown"
	}
	discoveredAt := "-"
	if result.ModelsDiscoveredAt != nil {
		discoveredAt = result.ModelsDiscoveredAt.UTC().Format(time.RFC3339)
	}
	message := strings.TrimSpace(result.ModelDiscoveryMessage)
	if message == "" {
		message = strings.TrimSpace(result.ErrorMessage)
	}
	log.Printf("[AutoDiscovery-Endpoint] channel=%s key=%s baseURL=%s source=%s models=%d discoveredAt=%s protocolOk=%t message=%s",
		channelUID, result.KeyMask, result.BaseURL, source, result.ModelsCount, discoveredAt, result.ProtocolOk, message)
}

func filterExcludedDiscoveryModels(models []string, patterns []string) []string {
	if len(models) == 0 || len(patterns) == 0 {
		return models
	}

	excludeRules := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		rule, err := regexp.Compile(pattern)
		if err != nil {
			log.Printf("[AutoDiscovery-ModelsFilter] 忽略非法排除正则 %q: %v", pattern, err)
			continue
		}
		excludeRules = append(excludeRules, rule)
	}
	if len(excludeRules) == 0 {
		return models
	}

	filtered := make([]string, 0, len(models))
	for _, modelID := range models {
		excluded := false
		for _, rule := range excludeRules {
			if rule.MatchString(modelID) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, modelID)
		}
	}
	return filtered
}

// parseModelsDeclaredEndpointTypes 解析 new-api 系上游在 /v1/models 中声明的
// supported_endpoint_types，返回 模型 -> 已声明协议集合。
//
// 该字段只作为协议探测的排序提示，不作为过滤依据：实测发现上游会少报。
// 例如 ooioo.work 的 gpt-5.5 只声明 ["openai"]，但 POST /v1/messages 实际返回 200
// （new-api 会把 OpenAI 模型跨协议转换成 Claude/Gemini 格式）。若当成白名单过滤，
// 会漏掉这些确实可用的模型，因此仅用于把已声明的模型优先排到探测队列前面。
func parseModelsDeclaredEndpointTypes(body []byte) map[string][]string {
	var resp struct {
		Data []struct {
			ID                     string   `json:"id"`
			SupportedEndpointTypes []string `json:"supported_endpoint_types"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	declared := make(map[string][]string)
	for _, m := range resp.Data {
		if m.ID == "" || len(m.SupportedEndpointTypes) == 0 {
			continue
		}
		protocols := make([]string, 0, len(m.SupportedEndpointTypes))
		for _, endpointType := range m.SupportedEndpointTypes {
			if protocol := protocolForEndpointType(endpointType); protocol != "" {
				protocols = append(protocols, protocol)
			}
		}
		if len(protocols) > 0 {
			declared[m.ID] = protocols
		}
	}
	if len(declared) == 0 {
		return nil
	}
	return declared
}

// protocolForEndpointType 把 new-api 的 endpoint type 枚举映射为 CCX 协议名。
// 枚举取值见 new-api constant/endpoint_type.go：openai / openai-response /
// openai-response-compact / anthropic / gemini / jina-rerank / image-generation /
// embeddings / openai-video；此处只关心参与协议探测的四种。
func protocolForEndpointType(endpointType string) string {
	switch strings.ToLower(strings.TrimSpace(endpointType)) {
	case "openai":
		return "chat"
	case "openai-response", "openai-response-compact":
		return "responses"
	case "anthropic":
		return "messages"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}

// parseModelsResponse 解析 OpenAI /v1/models 响应体。
func parseModelsResponse(body []byte) []string {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	models := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	for _, m := range resp.Models {
		modelID := strings.TrimSpace(m.Name)
		if index := strings.LastIndex(modelID, "/"); index >= 0 {
			modelID = modelID[index+1:]
		}
		if modelID != "" {
			models = append(models, modelID)
		}
	}
	return models
}

// writeProfiles 将发现结果写入 KeyEndpointProfile。
// MVP：只更新 ModelListHash / AvailableModels / Source / UpdatedAt，不修改 modelMapping。
// Phase 3B-2：同时为 autoManaged 渠道写入每发现模型的 ModelProfile 行（仅当 modelProfileStore != nil）。
func (r *AutoDiscoveryRunner) writeProfiles(channelUID string, channel *config.UpstreamConfig, endpoints []EndpointDiscoveryResult, cfgManager *config.ConfigManager) {
	if r.store == nil {
		return
	}

	// Phase 3B-2：准备全局上游模型能力和渠道类型（用于 model_profiles 填充）
	var globalModelCapabilities map[string]config.UpstreamModelCapability
	var channelKind string
	var channelID int
	if cfgManager != nil {
		cfg := cfgManager.GetConfig()
		globalModelCapabilities = cfg.UpstreamModelCapabilities
		var idx int
		idx, channelKind = findChannelIndexAndKind(cfg, channelUID)
		if idx >= 0 {
			channelID = idx
		}
	}

	for _, ep := range endpoints {
		if !ep.ProtocolOk {
			continue
		}
		if _, err := r.writeProfileForEndpoint(channelUID, channel, ep, channelID, channelKind, globalModelCapabilities); err != nil {
			log.Printf("[AutoDiscovery-Profile] 写入画像失败 endpoint=%s: %v", ep.KeyMask, err)
		}
	}

	// 自动发现完成即持久化，避免等待下一轮 L1 画像刷新期间进程退出而丢失结果。
	if err := r.flushStores(); err != nil {
		log.Printf("[AutoDiscovery-Profile] 画像落盘失败 channel=%s: %v", channelUID, err)
	}
}

// writeProfileForEndpoint 写入单个成功端点的 KeyEndpointProfile 与（自动托管时）ModelProfile。
// 不调用 Flush（由调用方批量 flush）。返回 endpointUID 与写入错误（Upsert 失败时返回 error）。
// checkpoint 路径用其返回值决定是否 SaveCheckpoint：只有 Upsert 成功才算可标记 profilePersisted。
func (r *AutoDiscoveryRunner) writeProfileForEndpoint(channelUID string, channel *config.UpstreamConfig, ep EndpointDiscoveryResult, channelID int, channelKind string, globalModelCapabilities map[string]config.UpstreamModelCapability) (string, error) {
	if r.store == nil {
		return "", nil
	}
	if !ep.ProtocolOk {
		return "", nil
	}
	// 从 channel 的 APIKeys 中找到对应 key
	apiKey := ep.apiKey
	if apiKey == "" {
		for _, key := range channel.APIKeys {
			if utils.MaskAPIKey(key) == ep.KeyMask {
				apiKey = key
				break
			}
		}
	}
	if apiKey == "" {
		return "", nil
	}

	keyHash := KeyHashFromAPIKey(apiKey)
	canonicalBaseURL := utils.CanonicalBaseURL(ep.BaseURL, channel.ServiceType)
	metricsKey := computeMetricsIdentityKey(canonicalBaseURL, apiKey, channel.ServiceType)
	endpointUID := GenerateEndpointUID(channelUID, canonicalBaseURL, keyHash)

	existing := r.store.Get(endpointUID)
	var profile KeyEndpointProfile
	if existing != nil {
		profile = *existing
	}
	profile.EndpointUID = endpointUID
	profile.AccountUID = channel.AccountUID
	profile.ChannelUID = channelUID
	profile.ChannelID = channelID
	if channelKind != "" {
		profile.ChannelKind = channelKind
	}
	if channel.ServiceType != "" {
		profile.ServiceType = channel.ServiceType
	}
	if profile.HealthState == "" {
		profile.HealthState = HealthStateUnknown
	}
	if profile.QualityTier == "" {
		profile.QualityTier = QualityTierNormal
	}
	if profile.StabilityTier == "" {
		profile.StabilityTier = StabilityTierNormal
	}
	if profile.SpeedTier == "" {
		profile.SpeedTier = SpeedTierNormal
	}
	if profile.CostTier == "" {
		profile.CostTier = CostTierNormal
	}
	profile.BaseURL = canonicalBaseURL
	profile.IdentityBaseURL = utils.MetricsIdentityBaseURL(canonicalBaseURL, channel.ServiceType)
	profile.KeyMask = ep.KeyMask
	profile.KeyHash = keyHash
	profile.MetricsKey = metricsKey
	profile.CredentialUID = ep.credentialUID
	// 写入能力 UID，供跨账号共享能力认知与探测去重。
	profile.CapabilityUID = r.capabilityUIDForResult(channel, &ep)
	if profile.CredentialUID == "" {
		profile.CredentialUID = channel.CredentialUIDForKey(apiKey)
	}
	profile.AvailableModels = ep.Models
	ensureConfiguredProtocolDiscovery(channel, &ep)
	profile.ProtocolModels = cloneProtocolModels(ep.ProtocolModels)
	profile.ProtocolModelsHash = hashProtocolModels(ep.ProtocolModels)
	profile.ProtocolDiscoveredAt = cloneTimeMap(ep.ProtocolDiscoveredAt)
	profile.ProtocolDiscoverySource = cloneStringMap(ep.ProtocolDiscoverySource)
	profile.ProtocolDiscoveryMessage = cloneStringMap(ep.ProtocolDiscoveryMessage)
	profile.ProtocolDiscoveryError = cloneStringMap(ep.ProtocolDiscoveryError)
	if len(ep.Models) > 0 {
		hash := sha256.Sum256([]byte(strings.Join(ep.Models, ",")))
		profile.ModelListHash = hex.EncodeToString(hash[:8])
	}
	if ep.ModelDiscoverySource != "" {
		profile.ModelDiscoverySource = ep.ModelDiscoverySource
	}
	if ep.ModelDiscoveryMessage != "" {
		profile.ModelDiscoveryMessage = ep.ModelDiscoveryMessage
	}
	if ep.ModelsDiscoveredAt != nil {
		discoveredAt := ep.ModelsDiscoveredAt.UTC()
		profile.ModelsDiscoveredAt = &discoveredAt
	}
	profile.Source = "auto_discovery"
	profile.UpdatedAt = time.Now()

	if err := r.store.Upsert(&profile); err != nil {
		return endpointUID, err
	}

	// Phase 3B-2：写入每个发现模型的 ModelProfile 行
	if r.ModelProfileStore != nil && channel.AutoManaged && len(ep.Models) > 0 {
		now := time.Now()
		for _, modelID := range ep.Models {
			family := InferModelFamily(modelID, "")
			qualityTier := ModelProfileQualityTier(modelID, family)

			modelProfile := &ModelProfile{
				ChannelUID:   channelUID,
				ChannelID:    channelID,
				ChannelKind:  channelKind,
				ServiceType:  channel.ServiceType,
				MetricsKey:   metricsKey,
				ModelID:      modelID,
				UpdatedAt:    now,
				ModelFamily:  family,
				QualityTier:  qualityTier,
				ProbeSuccess: true,
				Source:       "auto_discovery",
			}
			if resolved := config.ResolveUpstreamCapability(modelID, channel, globalModelCapabilities); resolved.Known {
				applyUpstreamModelCapability(modelProfile, resolved.Capability)
			}
			if existing := r.ModelProfileStore.Get(channelUID, channelKind, metricsKey, modelID); existing != nil {
				modelProfile.ProviderQualityScore = existing.ProviderQualityScore
				modelProfile.ProviderQualitySource = existing.ProviderQualitySource
				modelProfile.ProviderQualityConfidence = existing.ProviderQualityConfidence
				modelProfile.ProviderQualityProbeVersion = existing.ProviderQualityProbeVersion
				modelProfile.LastProbeAt = existing.LastProbeAt
				modelProfile.ProbeLatencyMs = existing.ProbeLatencyMs
				modelProfile.ProbeConfidence = existing.ProbeConfidence
			}
			if err := r.ModelProfileStore.Upsert(modelProfile); err != nil {
				log.Printf("[AutoDiscovery-ModelProfile] 写入模型画像失败 channel=%s model=%s: %v",
					channelUID, modelID, err)
			}
		}
	}
	return endpointUID, nil
}

// flushStores 强制把 ProfileStore 与 ModelProfileStore 的脏数据落盘。
// checkpoint 路径在每端点后调用以“先 Flush、后 checkpoint”。
func (r *AutoDiscoveryRunner) flushStores() error {
	if r.store == nil {
		return nil
	}
	var firstErr error
	if err := r.store.Flush(); err != nil {
		firstErr = err
	}
	if r.ModelProfileStore != nil {
		if err := r.ModelProfileStore.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// maybeAutoWriteChannelConfig 在发现完成后，检查是否可以将一致模型列表写入渠道配置。
// 安全守则：
//  1. 仅当所有成功探测的 endpoint 返回完全相同的模型列表（集合相等，顺序无关）时才写入
//  2. 不覆盖用户已有的手动配置（SupportedModels 或 ModelMapping 非空时不写入）
//  3. ModelMapping 不自动写入（比 SupportedModels 更容易出错，留给用户手动确认）
func (r *AutoDiscoveryRunner) maybeAutoWriteChannelConfig(channelUID string, channel *config.UpstreamConfig, endpoints []EndpointDiscoveryResult, cfgManager *config.ConfigManager) {
	// cfgManager 为 nil 时直接返回（runDiscovery 入口已有 guard，此处防御直接调用）
	if cfgManager == nil {
		return
	}
	// 自动托管账号的模型可用性属于具体 binding，已写入 KeyEndpointProfile。
	// 不再回写渠道级 SupportedModels，避免多 Key 权限不一致时丢失可用候选。
	if channel != nil && channel.AutoManaged {
		log.Printf("[AutoDiscovery-ConfigSkip] 渠道 %s: 自动托管模型由 endpoint profile 持久化，不写渠道级 SupportedModels", channelUID)
		return
	}

	// 收集所有成功探测的 endpoint 的模型列表
	var okEndpoints []EndpointDiscoveryResult
	for _, ep := range endpoints {
		if ep.ProtocolOk && len(ep.Models) > 0 {
			okEndpoints = append(okEndpoints, ep)
		}
	}

	// 无成功探测结果，不写
	if len(okEndpoints) == 0 {
		log.Printf("[AutoDiscovery-ConfigSkip] 渠道 %s: 无可达端点或模型列表为空，跳过自动写入", channelUID)
		return
	}

	// 检查一致性：所有成功探测的 endpoint 返回的模型列表集合必须完全相同
	consistentModels := modelsSetConsistent(okEndpoints)
	if consistentModels == nil {
		log.Printf("[AutoDiscovery-ConfigSkip] 渠道 %s: 端点模型列表不一致（%d 个可达端点），跳过自动写入",
			channelUID, len(okEndpoints))
		return
	}

	// 检查用户已有配置：SupportedModels 或 ModelMapping 非空时不覆盖
	if len(channel.SupportedModels) > 0 {
		log.Printf("[AutoDiscovery-ConfigSkip] 渠道 %s: 用户已配置 SupportedModels（%d 项），不覆盖",
			channelUID, len(channel.SupportedModels))
		return
	}
	if len(channel.ModelMapping) > 0 {
		log.Printf("[AutoDiscovery-ConfigSkip] 渠道 %s: 用户已配置 ModelMapping（%d 项），不覆盖 SupportedModels",
			channelUID, len(channel.ModelMapping))
		return
	}

	// 通过 ConfigManager 更新 SupportedModels
	// 先从当前配置中找到该渠道的 index 和 kind
	cfg := cfgManager.GetConfig()
	index, kind := findChannelIndexAndKind(cfg, channelUID)
	if index < 0 || kind == "" {
		log.Printf("[AutoDiscovery-ConfigSkip] 渠道 %s: 在当前配置中未找到对应渠道，跳过写入", channelUID)
		return
	}

	// 排序后写入，确保结果稳定可读
	sorted := sortModels(consistentModels)

	update := config.UpstreamUpdate{
		SupportedModels: sorted,
	}

	_, err := updateChannelByKind(cfgManager, kind, index, update)
	if err != nil {
		log.Printf("[AutoDiscovery-ConfigWrite] 渠道 %s: 写入 SupportedModels 失败: %v", channelUID, err)
		return
	}

	log.Printf("[AutoDiscovery-ConfigWrite] 渠道 %s: 已自动写入 SupportedModels（%d 项模型）",
		channelUID, len(sorted))

	// Phase 3A：发布 auto_mapping_applied 事件（只读展示，不影响调度）
	if r.hub != nil {
		now := time.Now()
		ev := ProfileChangeEvent{
			ChannelUID:  channelUID,
			ChannelKind: kind,
			EventType:   EventTypeAutoMappingApply,
			Summary:     fmt.Sprintf("自动写入 SupportedModels（%d 项模型）", len(sorted)),
			CreatedAt:   now,
		}
		ev.EventUID = GenerateChangeEventUID(channelUID, ev.EventType, now)
		r.hub.Publish(ev)
	}
}

// maybeLearnClientFingerprint 学习客户端伪装标记：任一端点裸请求被上游客户端指纹校验
// 拒绝、带 Claude Code 伪装头重试成功时，把渠道标记为 LearnedClientFingerprint，
// 后续该渠道的探测、拉模型、保活请求首发即带伪装头，不再裸试。
// 与 SupportedModels 安全守则无关（单字段 PATCH、不覆盖用户配置），自动托管渠道也写。
func (r *AutoDiscoveryRunner) maybeLearnClientFingerprint(channelUID string, channel *config.UpstreamConfig, endpoints []EndpointDiscoveryResult, cfgManager *config.ConfigManager) {
	if cfgManager == nil || channel == nil || channel.LearnedClientFingerprint {
		return
	}
	learned := false
	for _, ep := range endpoints {
		if ep.usedClientFingerprint {
			learned = true
			break
		}
	}
	if !learned {
		return
	}
	cfg := cfgManager.GetConfig()
	index, kind := findChannelIndexAndKind(cfg, channelUID)
	if index < 0 || kind == "" {
		log.Printf("[AutoDiscovery-ConfigSkip] 渠道 %s: 在当前配置中未找到对应渠道，跳过学习客户端伪装标记", channelUID)
		return
	}
	flag := true
	if _, err := updateChannelByKind(cfgManager, kind, index, config.UpstreamUpdate{LearnedClientFingerprint: &flag}); err != nil {
		log.Printf("[AutoDiscovery-ConfigWrite] 渠道 %s: 学习客户端伪装标记失败: %v", channelUID, err)
		return
	}
	channel.LearnedClientFingerprint = true
	log.Printf("[AutoDiscovery-ConfigWrite] 渠道 %s: 检测到上游客户端指纹校验，已学习客户端伪装标记", channelUID)
}

// modelsSetConsistent 检查所有 endpoint 的模型列表是否集合相等。
// 如果一致，返回任意一个端点的模型列表作为代表；如果不一致，返回 nil。
func modelsSetConsistent(endpoints []EndpointDiscoveryResult) []string {
	if len(endpoints) == 0 {
		return nil
	}

	// 将第一个端点的模型列表转为 set 作为基准
	baseSet := makeStringSet(endpoints[0].Models)

	for _, ep := range endpoints[1:] {
		candidateSet := makeStringSet(ep.Models)
		if !stringSetsEqual(baseSet, candidateSet) {
			return nil
		}
	}

	return endpoints[0].Models
}

// makeStringSet 将字符串列表转为 set（map[string]bool）。
func makeStringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

// stringSetsEqual 判断两个 string set 是否完全相同。
func stringSetsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// sortModels 对模型列表排序，确保写入结果稳定可读。
func sortModels(models []string) []string {
	sorted := make([]string, len(models))
	copy(sorted, models)
	sort.Strings(sorted)
	return sorted
}

// findChannelIndexAndKind 在当前配置中根据 channelUID 找到该渠道的 index 和 kind（渠道类型）。
// 返回 (-1, "") 表示未找到。
func findChannelIndexAndKind(cfg config.Config, channelUID string) (int, string) {
	type sliceKind struct {
		channels []config.UpstreamConfig
		kind     string
	}
	slices := []sliceKind{
		{cfg.Upstream, "messages"},
		{cfg.ChatUpstream, "chat"},
		{cfg.ResponsesUpstream, "responses"},
		{cfg.GeminiUpstream, "gemini"},
		{cfg.ImagesUpstream, "images"},
		{cfg.VectorsUpstream, "vectors"},
	}
	for _, sk := range slices {
		for i, ch := range sk.channels {
			if ch.ChannelUID == channelUID {
				return i, sk.kind
			}
		}
	}
	return -1, ""
}

// findChannelByUID 在所有 kind 下按 channelUID 查找渠道配置（找不到返回 nil）。
func findChannelByUID(cfg config.Config, channelUID string) *config.UpstreamConfig {
	type sliceKind struct {
		channels []config.UpstreamConfig
	}
	slices := []sliceKind{
		{cfg.Upstream},
		{cfg.ChatUpstream},
		{cfg.ResponsesUpstream},
		{cfg.GeminiUpstream},
		{cfg.ImagesUpstream},
		{cfg.VectorsUpstream},
	}
	for _, sk := range slices {
		for i := range sk.channels {
			if sk.channels[i].ChannelUID == channelUID {
				return &sk.channels[i]
			}
		}
	}
	return nil
}

// updateChannelByKind 根据渠道类型调用对应的 ConfigManager 更新方法。
func updateChannelByKind(cfgManager *config.ConfigManager, kind string, index int, update config.UpstreamUpdate) (bool, error) {
	switch kind {
	case "messages":
		return cfgManager.UpdateUpstream(index, update)
	case "chat":
		return cfgManager.UpdateChatUpstream(index, update)
	case "responses":
		return cfgManager.UpdateResponsesUpstream(index, update)
	case "gemini":
		return cfgManager.UpdateGeminiUpstream(index, update)
	case "images":
		return cfgManager.UpdateImagesUpstream(index, update)
	case "vectors":
		return cfgManager.UpdateVectorsUpstream(index, update)
	default:
		return false, fmt.Errorf("不支持的渠道类型: %s", kind)
	}
}

// computeMetricsIdentityKey 内联计算 MetricsIdentityKey，
// 与 metrics.GenerateMetricsIdentityKey 逻辑完全一致，避免 autopilot → metrics 循环导入。
func computeMetricsIdentityKey(baseURL, apiKey, serviceType string) string {
	normalized := utils.MetricsIdentityBaseURL(baseURL, serviceType)
	h := sha256.New()
	h.Write([]byte(normalized + "|" + apiKey))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
