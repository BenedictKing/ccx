package config

import (
	"fmt"
	"log"
)

func (cm *ConfigManager) SetImagesChannelStatus(index int, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("invalid Images upstream index: %d", index)
	}

	cm.config.ImagesUpstream[index].Status = status
	if status == "suspended" && cm.config.ImagesUpstream[index].PromotionUntil != nil {
		cm.config.ImagesUpstream[index].PromotionUntil = nil
		log.Printf("[Config-Status] cleared Images channel [%d] %s promotion period", index, cm.config.ImagesUpstream[index].Name)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}
	log.Printf("[Config-Status] set Images channel [%d] %s status to: %s", index, cm.config.ImagesUpstream[index].Name, status)
	return nil
}
