package telemetry

import "time"

// StructuredLog represents the JSON structure of logs emitted
// to Loki/slog containing trace details, latency metrics, and DB metadata.
type StructuredLog struct {
	// Base slog Fields
	Timestamp time.Time `json:"time"`
	Level     string    `json:"level"`
	Message   string    `json:"msg"`
	Logger    string    `json:"logger"` // e.g. "app", "media-engine"

	// OpenTelemetry Trace Context
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`

	// Execution Metrics
	Duration             string `json:"duration,omitempty"`              // e.g. "3.51ms", "2.14s"
	DurationMS           int64  `json:"duration_ms,omitempty"`           // Raw milliseconds for aggregation
	PostSpeechLatencyMS  int64  `json:"post_speech_latency_ms,omitempty"` // User end-of-speech to agent response latency

	// Context Metadata
	SessionID string `json:"session_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`

	// MongoDB Trace Attributes
	DBSystem     string `json:"db.system,omitempty"`     // e.g. "mongodb"
	DBCollection string `json:"db.collection,omitempty"` // e.g. "transactions"
	DBOperation  string `json:"db.operation,omitempty"`  // e.g. "find_many"
	DBLimit      int64  `json:"db.limit,omitempty"`

	// Redis Cache Trace Attributes
	RedisKeyType   string `json:"redis.key_type,omitempty"`   // e.g. "session_context"
	RedisOperation string `json:"redis.operation,omitempty"`  // e.g. "get", "set"
	RedisStream    string `json:"redis.stream,omitempty"`     // e.g. "audit_log_stream"
	RedisEventType string `json:"redis.event_type,omitempty"` // e.g. "dispatch", "warm_outcome"

	// Qdrant Vector DB Trace Attributes
	QdrantCollection string `json:"qdrant.collection,omitempty"` // e.g. "action_intents", "faq_items"

	// MCP Tool Execution Trace Attributes
	MCPTool string `json:"mcp.tool,omitempty"` // e.g. "get_balance", "transfer"

	// Ollama LLM Trace Attributes
	OllamaModel       string `json:"ollama.model,omitempty"`
	OllamaNumMessages int    `json:"ollama.num_messages,omitempty"`
}
