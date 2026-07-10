package db

import (
	"banking-voice-ai-agent/internal/telemetry"

	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"banking-voice-ai-agent/internal/ollama"

	"github.com/redis/go-redis/v9"
)

type RedisManager struct {
	Client *redis.Client
}

func NewRedisManager(addr string) (*RedisManager, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisManager{Client: client}, nil
}

// GetSessionContext fetches conversation context from Redis
func (r *RedisManager) GetSessionContext(ctx context.Context, sessionID string) ([]ollama.ChatMessage, error) {
	ctx, span := telemetry.Step(ctx, "redis.get_session")
	defer span.End()
	key := fmt.Sprintf("session:%s:context", sessionID)
	data, err := r.Client.Get(ctx, key).Result()
	if err == redis.Nil {
		return []ollama.ChatMessage{}, nil // empty context
	} else if err != nil {
		return nil, err
	}

	var messages []ollama.ChatMessage
	if err := json.Unmarshal([]byte(data), &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// SaveSessionContext saves the conversation history context to Redis with a TTL of 1 hour
func (r *RedisManager) SaveSessionContext(ctx context.Context, sessionID string, messages []ollama.ChatMessage) error {
	ctx, span := telemetry.Step(ctx, "redis.save_session")
	defer span.End()
	key := fmt.Sprintf("session:%s:context", sessionID)
	data, err := json.Marshal(messages)
	if err != nil {
		return err
	}

	err = r.Client.Set(ctx, key, data, 1*time.Hour).Err()
	if err != nil {
		return err
	}
	return nil
}

// ClearSessionContext deletes the conversation context for the session
func (r *RedisManager) ClearSessionContext(ctx context.Context, sessionID string) error {
	key := fmt.Sprintf("session:%s:context", sessionID)
	return r.Client.Del(ctx, key).Err()
}

// AddAuditLog appends an event to the audit stream (stand-in for Kafka)
func (r *RedisManager) AddAuditLog(ctx context.Context, turnID string, eventName string, payload map[string]any) error {
	ctx, span := telemetry.Step(ctx, "redis.audit")
	defer span.End()
	streamKey := "audit_log_stream"

	// Marshal payload to string
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	values := map[string]interface{}{
		"turn_id":   turnID,
		"event":     eventName,
		"payload":   string(payloadJSON),
		"timestamp": time.Now().Format(time.RFC3339Nano),
	}

	err = r.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: values,
	}).Err()

	if err != nil {
		log.Printf("Warning: failed to write to Redis Stream audit log: %v", err)
	}

	return err
}
