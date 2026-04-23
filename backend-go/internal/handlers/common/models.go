package common

import (
	"encoding/json"
	"strings"
)

type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

func BuildManualModelsResponse(models []string) ModelsResponse {
	entries := make([]ModelEntry, 0)
	seen := make(map[string]struct{}, len(models))

	for _, model := range models {
		modelID := strings.TrimSpace(model)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		entries = append(entries, ModelEntry{
			ID:      modelID,
			Object:  "model",
			Created: 0,
			OwnedBy: "ccx",
		})
	}

	return ModelsResponse{
		Object: "list",
		Data:   entries,
	}
}

func MarshalManualModelsResponse(models []string) ([]byte, error) {
	return json.Marshal(BuildManualModelsResponse(models))
}

func FindManualModelEntry(models []string, modelID string) (ModelEntry, bool) {
	for _, entry := range BuildManualModelsResponse(models).Data {
		if entry.ID == modelID {
			return entry, true
		}
	}
	return ModelEntry{}, false
}
