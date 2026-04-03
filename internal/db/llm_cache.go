package db

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// HashPrompt creates a SHA256 hash of the prompt for cache key
func HashPrompt(system, user string) string {
	h := sha256.New()
	h.Write([]byte(system + "|||" + user))
	return hex.EncodeToString(h.Sum(nil))
}

// GetCachedLLMResponse retrieves a cached LLM response JSON if it exists and hasn't expired
func (db *DB) GetCachedLLMResponse(promptHash, task string) (string, error) {
	var cached LLMResponseCache
	err := db.Where("prompt_hash = ? AND task = ? AND expires_at > ?", promptHash, task, time.Now()).
		First(&cached).Error
	if err != nil {
		return "", err
	}
	return cached.ResponseJSON, nil
}

// SetCachedLLMResponse stores an LLM response JSON in the cache
func (db *DB) SetCachedLLMResponse(promptHash, task, provider, model, responseJSON string, ttlHours int) error {
	cached := LLMResponseCache{
		Provider:     provider,
		Model:        model,
		Task:         task,
		PromptHash:   promptHash,
		ResponseJSON: responseJSON,
		ExpiresAt:    time.Now().Add(time.Duration(ttlHours) * time.Hour),
	}
	return db.Create(&cached).Error
}

// PruneExpiredLLMCache removes expired cache entries
func (db *DB) PruneExpiredLLMCache() error {
	return db.Where("expires_at < ?", time.Now()).Delete(&LLMResponseCache{}).Error
}
