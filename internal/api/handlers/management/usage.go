package management

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/redisqueue"
)

const defaultUsageDrainLimit = 10000

type usageTokenStats struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

type usageRequestDetail struct {
	Timestamp time.Time       `json:"timestamp"`
	LatencyMs int64           `json:"latency_ms"`
	Source    string          `json:"source"`
	AuthIndex string          `json:"auth_index"`
	Tokens    usageTokenStats `json:"tokens"`
	Failed    bool            `json:"failed"`
}

type queuedUsageRecord struct {
	usageRequestDetail
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Endpoint  string `json:"endpoint"`
	AuthType  string `json:"auth_type"`
	APIKey    string `json:"api_key"`
	RequestID string `json:"request_id"`
}

type usageModelBucket struct {
	TotalRequests int64                `json:"total_requests"`
	TotalTokens   int64                `json:"total_tokens"`
	Details       []usageRequestDetail `json:"details"`
}

type usageAPIBucket struct {
	TotalRequests int64                        `json:"total_requests"`
	TotalTokens   int64                        `json:"total_tokens"`
	SuccessCount  int64                        `json:"success_count"`
	FailureCount  int64                        `json:"failure_count"`
	InputTokens   int64                        `json:"input_tokens"`
	OutputTokens  int64                        `json:"output_tokens"`
	Models        map[string]*usageModelBucket `json:"models"`
}

type usageResponse struct {
	TotalRequests int64                      `json:"total_requests"`
	SuccessCount  int64                      `json:"success_count"`
	FailureCount  int64                      `json:"failure_count"`
	TotalTokens   int64                      `json:"total_tokens"`
	APIs          map[string]*usageAPIBucket `json:"apis"`
}

// GetUsage drains detailed usage records from the in-memory usage queue and
// returns the legacy /v0/management/usage shape consumed by dashboard collectors.
func (h *Handler) GetUsage(c *gin.Context) {
	if !redisqueue.Enabled() {
		c.JSON(http.StatusOK, usageResponse{APIs: map[string]*usageAPIBucket{}})
		return
	}

	rawItems := redisqueue.PopOldest(defaultUsageDrainLimit)
	resp := usageResponse{APIs: make(map[string]*usageAPIBucket)}

	for _, raw := range rawItems {
		var record queuedUsageRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			continue
		}

		model := strings.TrimSpace(record.Model)
		if model == "" {
			model = "unknown"
		}

		apiGroup := strings.TrimSpace(record.APIKey)
		if apiGroup == "" {
			apiGroup = strings.TrimSpace(record.AuthIndex)
		}
		if apiGroup == "" {
			apiGroup = strings.TrimSpace(record.Source)
		}
		if apiGroup == "" {
			apiGroup = "unknown"
		}

		if record.Tokens.TotalTokens == 0 {
			record.Tokens.TotalTokens = record.Tokens.InputTokens + record.Tokens.OutputTokens + record.Tokens.ReasoningTokens
		}

		apiBucket := resp.APIs[apiGroup]
		if apiBucket == nil {
			apiBucket = &usageAPIBucket{Models: make(map[string]*usageModelBucket)}
			resp.APIs[apiGroup] = apiBucket
		}

		modelBucket := apiBucket.Models[model]
		if modelBucket == nil {
			modelBucket = &usageModelBucket{}
			apiBucket.Models[model] = modelBucket
		}

		apiBucket.TotalRequests++
		modelBucket.TotalRequests++
		resp.TotalRequests++

		apiBucket.TotalTokens += record.Tokens.TotalTokens
		apiBucket.InputTokens += record.Tokens.InputTokens
		apiBucket.OutputTokens += record.Tokens.OutputTokens
		modelBucket.TotalTokens += record.Tokens.TotalTokens
		resp.TotalTokens += record.Tokens.TotalTokens

		if record.Failed {
			apiBucket.FailureCount++
			resp.FailureCount++
		} else {
			apiBucket.SuccessCount++
			resp.SuccessCount++
		}

		modelBucket.Details = append(modelBucket.Details, record.usageRequestDetail)
	}

	c.JSON(http.StatusOK, resp)
}
