package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/types"
)

type Conversation struct {
	ID                   string     `json:"id"`
	Kind                 string     `json:"kind"`
	UserID               string     `json:"userId"`
	RawUserID            string     `json:"rawUserId,omitempty"`
	Title                string     `json:"title,omitempty"`
	GeneratedTitle       string     `json:"-"`
	FallbackTitle        string     `json:"-"`
	SessionID            string     `json:"-"`
	ParentThreadID       string     `json:"parentThreadId,omitempty"`
	ParentConversationID string     `json:"parentConversationId,omitempty"`
	ChildConversationIDs []string   `json:"childConversationIds,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	LastActiveAt         time.Time  `json:"lastActiveAt"`
	RequestCount         int        `json:"requestCount"`
	Models               []string   `json:"models"`
	CurrentChannel       int        `json:"currentChannel"`
	ChannelName          string     `json:"channelName"`
	Status               string     `json:"status"`
	LastModel            string     `json:"lastModel"`
	LastRequestID        string     `json:"lastRequestId"`
	LastUserMessage      string     `json:"lastUserMessage,omitempty"`
	LastUserMessages     []string   `json:"lastUserMessages,omitempty"`
	LastRecap            string     `json:"lastRecap,omitempty"`
	LastRecapAt          *time.Time `json:"lastRecapAt,omitempty"`

	// subagent 观测（仅展示，不影响路由）
	HasSubagents    bool `json:"hasSubagents,omitempty"`
	SubagentCount   int  `json:"subagentCount,omitempty"`
	MainChannel     int  `json:"mainChannel,omitempty"`
	SubagentChannel int  `json:"subagentChannel,omitempty"`

	// 成功渠道独立于流式进行中状态，用于恢复加密续轮的来源亲和。
	SuccessfulChannels map[string]ChannelEntry `json:"-"`
}

func (conv *Conversation) recomputeTitle() {
	if conv.GeneratedTitle != "" {
		const maxTitleRunes = 80
		if len([]rune(conv.GeneratedTitle)) < maxTitleRunes && conv.FallbackTitle != "" {
			conv.Title = composeTitleWithFallback(conv.GeneratedTitle, conv.FallbackTitle)
			return
		}
		conv.Title = truncateTitle(conv.GeneratedTitle)
		return
	}
	conv.Title = conv.FallbackTitle
}

type pendingTitle struct {
	title     string
	createdAt time.Time
}

type ConversationTracker struct {
	mu               sync.RWMutex
	conversations    map[string]*Conversation
	sessionMapping   map[string]string        // sessionID → conversationID (for Responses)
	userMapping      map[string]string        // kind:userID → conversationID (for Chat/Messages/Gemini)
	pendingTitles    map[string]*pendingTitle // kind:userID → title (title 请求先于对话创建时暂存)
	idleTTL          time.Duration
	expireTTL        time.Duration
	maxConversations int
	persistPath      string
	dirty            bool
	stopCh           chan struct{}
	stopOnce         sync.Once
}

func NewConversationTracker(idleTTL, expireTTL time.Duration, persistPath ...string) *ConversationTracker {
	path := ""
	if len(persistPath) > 0 {
		path = persistPath[0]
	}

	ct := &ConversationTracker{
		conversations:    make(map[string]*Conversation),
		sessionMapping:   make(map[string]string),
		userMapping:      make(map[string]string),
		pendingTitles:    make(map[string]*pendingTitle),
		idleTTL:          idleTTL,
		expireTTL:        expireTTL,
		maxConversations: 100,
		persistPath:      path,
		stopCh:           make(chan struct{}),
	}

	if path != "" {
		ct.loadFromDisk()
	}

	go ct.cleanupLoop()
	if path != "" {
		go ct.persistLoop()
	}
	return ct
}

func (ct *ConversationTracker) Track(kind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, userMessageCount int, agentRole string, agentCtx ...*types.AgentContext) {
	ct.TrackWithMessages(kind, userID, model, channelIndex, channelName, sessionID, lastUserMessage, nil, userMessageCount, agentRole, agentCtx...)
}

func (ct *ConversationTracker) TrackWithMessages(kind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, lastUserMessages []string, userMessageCount int, agentRole string, agentCtx ...*types.AgentContext) {
	ct.TrackWithStatusAndMessages(kind, userID, model, channelIndex, channelName, sessionID, lastUserMessage, lastUserMessages, userMessageCount, agentRole, "active", agentCtx...)
}

// TrackWithStatus 追踪对话并写入指定状态，用于流式请求开始时让驾驶舱立即可见。
func (ct *ConversationTracker) TrackWithStatus(kind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, userMessageCount int, agentRole, status string, agentCtx ...*types.AgentContext) {
	ct.TrackWithStatusAndMessages(kind, userID, model, channelIndex, channelName, sessionID, lastUserMessage, nil, userMessageCount, agentRole, status, agentCtx...)
}

func (ct *ConversationTracker) TrackWithStatusAndMessages(kind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, lastUserMessages []string, userMessageCount int, agentRole, status string, agentCtx ...*types.AgentContext) {
	if userID == "" {
		return
	}
	if status == "" {
		status = "active"
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	var ctx *types.AgentContext
	if len(agentCtx) > 0 {
		ctx = agentCtx[0]
	}

	convID := ct.resolveConversationID(kind, userID, sessionID)
	now := time.Now()

	conv, exists := ct.conversations[convID]
	if !exists {
		conv = &Conversation{
			ID:        convID,
			Kind:      kind,
			UserID:    maskUserID(userID),
			RawUserID: userID,
			CreatedAt: now,
			Status:    "active",
			Models:    []string{},
		}
		ct.conversations[convID] = conv

		if sessionID != "" {
			conv.SessionID = sessionID
			ct.sessionMapping[sessionID] = convID
		}
		compositeKey := kind + ":" + userID
		ct.userMapping[compositeKey] = convID

		// 应用先于对话创建到达的 pending title
		if pt, ok := ct.pendingTitles[compositeKey]; ok {
			conv.GeneratedTitle = pt.title
			conv.recomputeTitle()
			delete(ct.pendingTitles, compositeKey)
		}
	}

	conv.LastActiveAt = now
	if userMessageCount > 0 {
		conv.RequestCount = userMessageCount
	} else {
		conv.RequestCount++
	}
	conv.CurrentChannel = channelIndex
	conv.ChannelName = channelName
	conv.LastModel = model
	conv.Status = status
	if kind == "responses" && status == "active" {
		if conv.SuccessfulChannels == nil {
			conv.SuccessfulChannels = make(map[string]ChannelEntry)
		}
		conv.SuccessfulChannels[affinityRole(agentRole)] = ChannelEntry{ChannelIndex: channelIndex, ChannelName: channelName}
	}

	// 角色观测：subagent 单独累计，主对话记录主渠道
	if agentRole == "subagent" {
		conv.HasSubagents = true
		if status != "streaming" {
			conv.SubagentCount++
		}
		conv.SubagentChannel = channelIndex
	} else {
		conv.MainChannel = channelIndex
	}

	if !containsString(conv.Models, model) {
		conv.Models = append(conv.Models, model)
	}

	fullMessage := normalizeConversationContent(lastUserMessage)
	normalizedMessages := normalizeConversationMessages(lastUserMessages)
	if fullMessage != "" {
		if len(normalizedMessages) == 0 {
			normalizedMessages = []string{truncateConversationContent(fullMessage)}
		}
		conv.LastUserMessage = truncateConversationContent(fullMessage)
		conv.LastUserMessages = normalizedMessages
		conv.FallbackTitle = truncateTitle(fullMessage)
		conv.recomputeTitle()
	} else if len(normalizedMessages) > 0 {
		joinedMessage := strings.Join(normalizedMessages, " / ")
		conv.LastUserMessage = truncateConversationContent(joinedMessage)
		conv.LastUserMessages = normalizedMessages
		conv.FallbackTitle = truncateTitle(joinedMessage)
		conv.recomputeTitle()
	}

	if ctx != nil {
		parentThreadID := strings.TrimSpace(ctx.ParentThreadID)
		if parentThreadID != "" {
			conv.ParentThreadID = parentThreadID
		}
	}

	ct.reconcileConversationRelationshipsLocked(conv)
	ct.dirty = true
}

func (ct *ConversationTracker) UpdateTitle(kind, userID, title string) bool {
	if userID == "" || title == "" {
		return false
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	convID := ct.resolveConversationID(kind, userID, "")
	conv, exists := ct.conversations[convID]
	if !exists {
		// 对话尚未创建（title 请求先于正常请求到达），暂存 title
		compositeKey := kind + ":" + userID
		ct.pendingTitles[compositeKey] = &pendingTitle{
			title:     strings.TrimSpace(title),
			createdAt: time.Now(),
		}
		return true
	}

	conv.GeneratedTitle = strings.TrimSpace(title)
	conv.recomputeTitle()
	ct.dirty = true
	return true
}

func (ct *ConversationTracker) UpdateRecap(kind, userID, recap string) bool {
	if userID == "" {
		return false
	}

	recap = truncateConversationContent(normalizeConversationContent(recap))
	if recap == "" {
		return false
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	compositeKey := kind + ":" + userID
	convID, ok := ct.userMapping[compositeKey]
	if !ok {
		return false
	}
	conv, ok := ct.conversations[convID]
	if !ok {
		return false
	}

	now := time.Now()
	conv.LastRecap = recap
	conv.LastRecapAt = &now
	conv.LastActiveAt = now
	ct.dirty = true
	return true
}

func (ct *ConversationTracker) UpdateStatus(kind, userID, status string) {
	if userID == "" {
		return
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	compositeKey := kind + ":" + userID
	convID, ok := ct.userMapping[compositeKey]
	if !ok {
		return
	}
	conv, ok := ct.conversations[convID]
	if !ok {
		return
	}
	conv.Status = status
	conv.LastActiveAt = time.Now()
	ct.dirty = true
}

// UpdateStatusByID 按会话 ID 更新状态，用于请求生命周期结束后的状态回写。
func (ct *ConversationTracker) UpdateStatusByID(conversationID, status string) bool {
	if conversationID == "" {
		return false
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	conv, ok := ct.conversations[conversationID]
	if !ok {
		return false
	}
	conv.Status = status
	conv.LastActiveAt = time.Now()
	ct.dirty = true
	return true
}

func (ct *ConversationTracker) SetLastRequestID(kind, userID, requestID string) {
	if userID == "" {
		return
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	compositeKey := kind + ":" + userID
	convID, ok := ct.userMapping[compositeKey]
	if !ok {
		return
	}
	conv, ok := ct.conversations[convID]
	if !ok {
		return
	}
	conv.LastRequestID = requestID
	ct.dirty = true
}

func (ct *ConversationTracker) GetActiveConversations(kindFilter string) []*Conversation {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	result := make([]*Conversation, 0, len(ct.conversations))
	for _, conv := range ct.conversations {
		if kindFilter != "" && conv.Kind != kindFilter {
			continue
		}
		result = append(result, conv)
	}
	return result
}

func (ct *ConversationTracker) GetConversation(id string) (*Conversation, bool) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	conv, ok := ct.conversations[id]
	return conv, ok
}

func (ct *ConversationTracker) GetConversationByUser(kind, userID string) (*Conversation, bool) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	compositeKey := kind + ":" + userID
	convID, ok := ct.userMapping[compositeKey]
	if !ok {
		return nil, false
	}
	conv, ok := ct.conversations[convID]
	return conv, ok
}

// GetSuccessfulChannel 返回成功渠道的值快照，避免读取进行中请求覆盖的 CurrentChannel。
func (ct *ConversationTracker) GetSuccessfulChannel(kind, userID, agentRole string) (ChannelEntry, bool) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	conv := ct.conversations[ct.userMapping[kind+":"+userID]]
	if conv == nil {
		return ChannelEntry{}, false
	}
	channel, ok := conv.SuccessfulChannels[affinityRole(agentRole)]
	return channel, ok
}

func affinityRole(agentRole string) string {
	if agentRole == "subagent" {
		return "subagent"
	}
	return "main"
}

func (ct *ConversationTracker) Stop() {
	ct.stopOnce.Do(func() {
		close(ct.stopCh)
		if ct.persistPath != "" {
			ct.flushToDisk()
		}
	})
}

func (ct *ConversationTracker) resolveConversationID(kind, userID, sessionID string) string {
	if sessionID != "" {
		if convID, ok := ct.sessionMapping[sessionID]; ok {
			return convID
		}
		return sessionID
	}

	compositeKey := kind + ":" + userID
	if convID, ok := ct.userMapping[compositeKey]; ok {
		return convID
	}

	hash := sha256.Sum256([]byte(compositeKey))
	return "conv_" + hex.EncodeToString(hash[:6])
}

func (ct *ConversationTracker) loadFromDisk() {
	items, skipped, err := loadPersistedState(ct.persistPath, ct.expireTTL)
	if err != nil {
		log.Printf("[ConversationTracker-Load] 读取失败: %v", err)
		return
	}
	if len(items) == 0 && skipped == 0 {
		return
	}

	for _, item := range items {
		models := item.Models
		if models == nil {
			models = []string{}
		}

		conv := &Conversation{
			ID:                   item.ID,
			Kind:                 item.Kind,
			UserID:               item.UserID,
			RawUserID:            item.RawUserID,
			Title:                item.Title,
			GeneratedTitle:       item.GeneratedTitle,
			FallbackTitle:        item.FallbackTitle,
			SessionID:            item.SessionID,
			ParentThreadID:       item.ParentThreadID,
			ParentConversationID: item.ParentConversationID,
			ChildConversationIDs: append([]string(nil), item.ChildConversationIDs...),
			RequestCount:         item.RequestCount,
			Models:               models,
			CurrentChannel:       item.CurrentChannel,
			ChannelName:          item.ChannelName,
			LastModel:            item.LastModel,
			LastRequestID:        item.LastRequestID,
			LastUserMessage:      item.LastUserMessage,
			LastUserMessages:     append([]string(nil), item.LastUserMessages...),
			LastRecap:            item.LastRecap,
			LastRecapAt:          cloneTimePtr(item.LastRecapAt),
			HasSubagents:         item.HasSubagents,
			SubagentCount:        item.SubagentCount,
			MainChannel:          item.MainChannel,
			SubagentChannel:      item.SubagentChannel,
			SuccessfulChannels:   maps.Clone(item.SuccessfulChannels),
			CreatedAt:            item.CreatedAt,
			LastActiveAt:         item.LastActiveAt,
			Status:               "idle",
		}
		ct.conversations[item.ID] = conv

		compositeKey := item.Kind + ":" + item.RawUserID
		ct.userMapping[compositeKey] = item.ID
		if item.SessionID != "" {
			ct.sessionMapping[item.SessionID] = item.ID
		}
	}
	ct.reconcileAllConversationRelationshipsLocked()
	log.Printf("[ConversationTracker-Load] 恢复 %d 个对话, 跳过 %d 个过期条目", len(items), skipped)
}

func (ct *ConversationTracker) persistLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ct.stopCh:
			return
		case <-ticker.C:
			ct.flushToDisk()
		}
	}
}

func (ct *ConversationTracker) flushToDisk() {
	ct.mu.Lock()
	if !ct.dirty {
		ct.mu.Unlock()
		return
	}
	ct.dirty = false
	snapshot := make(map[string]*Conversation, len(ct.conversations))
	for id, conv := range ct.conversations {
		c := *conv
		c.SuccessfulChannels = maps.Clone(conv.SuccessfulChannels)
		if len(conv.Models) > 0 {
			c.Models = make([]string, len(conv.Models))
			copy(c.Models, conv.Models)
		}
		if len(conv.ChildConversationIDs) > 0 {
			c.ChildConversationIDs = make([]string, len(conv.ChildConversationIDs))
			copy(c.ChildConversationIDs, conv.ChildConversationIDs)
		}
		if len(conv.LastUserMessages) > 0 {
			c.LastUserMessages = make([]string, len(conv.LastUserMessages))
			copy(c.LastUserMessages, conv.LastUserMessages)
		}
		snapshot[id] = &c
	}
	ct.mu.Unlock()

	if err := savePersistedState(ct.persistPath, snapshot); err != nil {
		ct.mu.Lock()
		ct.dirty = true
		ct.mu.Unlock()
		log.Printf("[ConversationTracker-Save] 写入失败: %v", err)
	}
}

func (ct *ConversationTracker) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ct.stopCh:
			return
		case <-ticker.C:
			ct.cleanup()
		}
	}
}

func (ct *ConversationTracker) cleanup() {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now()
	var removed int

	for id, conv := range ct.conversations {
		idleDuration := now.Sub(conv.LastActiveAt)

		if idleDuration > ct.expireTTL {
			ct.removeConversation(id, conv)
			removed++
		} else if idleDuration > ct.idleTTL && conv.Status != "idle" {
			conv.Status = "idle"
		}
	}

	// 清理超时的 pending titles（超过 2 分钟未被消费则丢弃）
	const pendingTitleTTL = 2 * time.Minute
	for key, pt := range ct.pendingTitles {
		if now.Sub(pt.createdAt) > pendingTitleTTL {
			delete(ct.pendingTitles, key)
		}
	}

	// 数量上限裁剪：超过 maxConversations 时按 LastActiveAt 从旧到新删除
	if ct.maxConversations > 0 && len(ct.conversations) > ct.maxConversations {
		type entry struct {
			id   string
			conv *Conversation
		}
		entries := make([]entry, 0, len(ct.conversations))
		for id, conv := range ct.conversations {
			entries = append(entries, entry{id, conv})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].conv.LastActiveAt.Before(entries[j].conv.LastActiveAt)
		})
		excess := len(entries) - ct.maxConversations
		for i := 0; i < excess; i++ {
			ct.removeConversation(entries[i].id, entries[i].conv)
			removed++
		}
	}

	if removed > 0 {
		log.Printf("[ConversationTracker-Cleanup] 清理 %d 个过期对话, 剩余 %d", removed, len(ct.conversations))
	}
}

func (ct *ConversationTracker) removeConversation(id string, conv *Conversation) {
	delete(ct.conversations, id)

	compositeKey := conv.Kind + ":" + conv.RawUserID
	if ct.userMapping[compositeKey] == id {
		delete(ct.userMapping, compositeKey)
	}

	for sessID, convID := range ct.sessionMapping {
		if convID == id {
			delete(ct.sessionMapping, sessID)
		}
	}

	for _, other := range ct.conversations {
		if other == nil {
			continue
		}
		if other.ParentConversationID == id {
			other.ParentConversationID = ""
		}
		other.ChildConversationIDs = removeString(other.ChildConversationIDs, id)
	}
	ct.dirty = true
}

func (ct *ConversationTracker) reconcileAllConversationRelationshipsLocked() {
	for _, conv := range ct.conversations {
		conv.ParentConversationID = ""
		conv.ChildConversationIDs = nil
	}
	for _, conv := range ct.conversations {
		ct.reconcileConversationRelationshipsLocked(conv)
	}
}

func (ct *ConversationTracker) reconcileConversationRelationshipsLocked(conv *Conversation) {
	if conv == nil {
		return
	}

	if parentID, ok := ct.findConversationIDByThreadLocked(conv.ParentThreadID); ok && parentID != conv.ID {
		if parent, exists := ct.conversations[parentID]; exists {
			ct.linkParentChildLocked(parent, conv)
		}
	}

	for _, other := range ct.conversations {
		if other == nil || other.ID == conv.ID {
			continue
		}
		if other.ParentConversationID == conv.ID || other.ParentThreadID == conv.ID || other.ParentThreadID == conv.RawUserID || (conv.SessionID != "" && other.ParentThreadID == conv.SessionID) {
			ct.linkParentChildLocked(conv, other)
		}
	}
}

func (ct *ConversationTracker) findConversationIDByThreadLocked(threadID string) (string, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false
	}

	for id, conv := range ct.conversations {
		if id == threadID || conv.SessionID == threadID || conv.RawUserID == threadID {
			return id, true
		}
	}
	return "", false
}

func (ct *ConversationTracker) linkParentChildLocked(parent, child *Conversation) {
	if parent == nil || child == nil || parent.ID == "" || child.ID == "" || parent.ID == child.ID {
		return
	}

	ct.removeChildReferenceFromOtherParentsLocked(parent.ID, child.ID)
	if child.ParentConversationID != "" && child.ParentConversationID != parent.ID {
		child.ParentConversationID = ""
	}
	child.ParentConversationID = parent.ID
	if !containsString(parent.ChildConversationIDs, child.ID) {
		parent.ChildConversationIDs = append(parent.ChildConversationIDs, child.ID)
	}
	parent.HasSubagents = true
	if parent.SubagentCount < len(parent.ChildConversationIDs) {
		parent.SubagentCount = len(parent.ChildConversationIDs)
	}
}

func (ct *ConversationTracker) removeChildReferenceFromOtherParentsLocked(parentID, childID string) {
	if parentID == "" || childID == "" {
		return
	}
	for id, conv := range ct.conversations {
		if id == parentID || conv == nil {
			continue
		}
		conv.ChildConversationIDs = removeString(conv.ChildConversationIDs, childID)
	}
}

func removeString(slice []string, target string) []string {
	if len(slice) == 0 || target == "" {
		return slice
	}
	result := slice[:0]
	for _, item := range slice {
		if item != target {
			result = append(result, item)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func maskUserID(userID string) string {
	if len(userID) <= 8 {
		return userID[:1] + "***"
	}
	if idx := strings.Index(userID, "_session_"); idx >= 0 {
		sessionPart := userID[idx+9:]
		if len(sessionPart) > 8 {
			sessionPart = sessionPart[:8]
		}
		return "sess:" + sessionPart
	}
	if len(userID) > 20 {
		return userID[:8] + "..." + userID[len(userID)-4:]
	}
	return userID[:4] + "***" + userID[len(userID)-4:]
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cloned := *t
	return &cloned
}

func normalizeConversationContent(message string) string {
	msg := strings.ReplaceAll(message, "\r", "")
	msg = strings.TrimSpace(msg)
	return msg
}

func truncateConversationContent(content string) string {
	const maxContentRunes = 2000
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if len(runes) > maxContentRunes {
		return string(runes[:maxContentRunes]) + "..."
	}
	return content
}

func normalizeConversationMessages(messages []string) []string {
	if len(messages) == 0 {
		return nil
	}

	result := make([]string, 0, len(messages))
	for _, message := range messages {
		if normalized := truncateConversationContent(normalizeConversationContent(message)); normalized != "" {
			result = append(result, normalized)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func composeTitleWithFallback(title, fallback string) string {
	title = strings.TrimSpace(title)
	fallback = strings.TrimSpace(fallback)
	if title == "" || fallback == "" {
		return title
	}
	if title == fallback || strings.Contains(title, " — "+fallback) {
		return truncateTitle(title)
	}
	combined := title + " — " + fallback
	return truncateTitle(combined)
}

func truncateTitle(title string) string {
	const maxTitleRunes = 80
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	runes := []rune(title)
	if len(runes) > maxTitleRunes {
		return string(runes[:maxTitleRunes]) + "..."
	}
	return title
}
