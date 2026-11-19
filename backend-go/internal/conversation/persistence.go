package conversation

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type persistedConversation struct {
	ID                   string     `json:"id"`
	Kind                 string     `json:"kind"`
	UserID               string     `json:"userId"`
	RawUserID            string     `json:"rawUserId"`
	Title                string     `json:"title,omitempty"`
	GeneratedTitle       string     `json:"generatedTitle,omitempty"`
	FallbackTitle        string     `json:"fallbackTitle,omitempty"`
	SessionID            string     `json:"sessionId,omitempty"`
	ParentThreadID       string     `json:"parentThreadId,omitempty"`
	ParentConversationID string     `json:"parentConversationId,omitempty"`
	ChildConversationIDs []string   `json:"childConversationIds,omitempty"`
	RequestCount         int        `json:"requestCount"`
	CurrentChannel       int        `json:"currentChannel"`
	ChannelName          string     `json:"channelName,omitempty"`
	Models               []string   `json:"models,omitempty"`
	LastModel            string     `json:"lastModel,omitempty"`
	LastRequestID        string     `json:"lastRequestId,omitempty"`
	LastUserMessage      string     `json:"lastUserMessage,omitempty"`
	LastUserMessages     []string   `json:"lastUserMessages,omitempty"`
	LastRecap            string     `json:"lastRecap,omitempty"`
	LastRecapAt          *time.Time `json:"lastRecapAt,omitempty"`
	HasSubagents         bool       `json:"hasSubagents,omitempty"`
	SubagentCount        int        `json:"subagentCount,omitempty"`
	MainChannel          int        `json:"mainChannel,omitempty"`
	SubagentChannel      int        `json:"subagentChannel,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	LastActiveAt         time.Time  `json:"lastActiveAt"`

	SuccessfulChannels map[string]SuccessfulChannel `json:"successfulChannels,omitempty"`
}

type persistedState struct {
	Version       int                     `json:"version"`
	Conversations []persistedConversation `json:"conversations"`
	SavedAt       time.Time               `json:"savedAt"`
}

func loadPersistedState(path string, expireTTL time.Duration) ([]persistedConversation, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[ConversationTracker-Load] JSON 解析失败, 忽略: %v", err)
		return nil, 0, nil
	}

	now := time.Now()
	var valid []persistedConversation
	skipped := 0
	for _, c := range state.Conversations {
		if c.LastActiveAt.Add(expireTTL).Before(now) {
			skipped++
			continue
		}
		valid = append(valid, c)
	}
	return valid, skipped, nil
}

func savePersistedState(path string, conversations map[string]*Conversation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	items := make([]persistedConversation, 0, len(conversations))
	for _, conv := range conversations {
		items = append(items, persistedConversation{
			ID:                   conv.ID,
			Kind:                 conv.Kind,
			UserID:               conv.UserID,
			RawUserID:            conv.RawUserID,
			Title:                conv.Title,
			GeneratedTitle:       conv.GeneratedTitle,
			FallbackTitle:        conv.FallbackTitle,
			SessionID:            conv.SessionID,
			ParentThreadID:       conv.ParentThreadID,
			ParentConversationID: conv.ParentConversationID,
			ChildConversationIDs: append([]string(nil), conv.ChildConversationIDs...),
			RequestCount:         conv.RequestCount,
			CurrentChannel:       conv.CurrentChannel,
			ChannelName:          conv.ChannelName,
			Models:               conv.Models,
			LastModel:            conv.LastModel,
			LastRequestID:        conv.LastRequestID,
			LastUserMessage:      conv.LastUserMessage,
			LastUserMessages:     append([]string(nil), conv.LastUserMessages...),
			LastRecap:            conv.LastRecap,
			LastRecapAt:          cloneTimePtr(conv.LastRecapAt),
			HasSubagents:         conv.HasSubagents,
			SubagentCount:        conv.SubagentCount,
			MainChannel:          conv.MainChannel,
			SubagentChannel:      conv.SubagentChannel,
			SuccessfulChannels:   conv.SuccessfulChannels,
			CreatedAt:            conv.CreatedAt,
			LastActiveAt:         conv.LastActiveAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].LastActiveAt.After(items[j].LastActiveAt)
	})

	state := persistedState{
		Version:       1,
		Conversations: items,
		SavedAt:       time.Now(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
