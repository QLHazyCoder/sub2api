package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// newOpenAIStreamDataIntervalTimeoutError creates a request-scoped failure for
// an OpenAI Responses stream that produced no semantic output during the
// configured upstream data interval. It is intentionally independent of
// account runtime state: a quiet model stream is not evidence of bad account
// credentials or account health.
func (s *OpenAIGatewayService) newOpenAIStreamDataIntervalTimeoutError(
	c *gin.Context,
	account *Account,
	passthrough bool,
	upstreamRequestID string,
	originalModel string,
	interval time.Duration,
	responseHeaders http.Header,
) *UpstreamFailoverError {
	platform := PlatformOpenAI
	accountID := int64(0)
	accountName := ""
	if account != nil {
		platform = account.Platform
		accountID = account.ID
		accountName = account.Name
	}
	message := "OpenAI upstream produced no semantic output during the stream data interval"
	detail := fmt.Sprintf("model=%s interval_ms=%d", strings.TrimSpace(originalModel), interval.Milliseconds())
	setOpsUpstreamError(c, http.StatusGatewayTimeout, message, detail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           platform,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: http.StatusGatewayTimeout,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Passthrough:        passthrough,
		Kind:               "stream_data_interval_timeout",
		Scope:              string(GatewayFailureScopeRequest),
		Reason:             string(openAIStreamDataIntervalTimeoutReason),
		Message:            message,
		Detail:             detail,
	})
	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "stream_data_interval_timeout",
			"message": message,
		},
	})
	return &UpstreamFailoverError{
		StatusCode:               http.StatusGatewayTimeout,
		ResponseBody:             body,
		ResponseHeaders:          responseHeaders.Clone(),
		SafeToFailoverAfterWrite: true,
		RetryableOnSameAccount:   false,
		RequestScopedTransient:   false,
		Scope:                    GatewayFailureScopeRequest,
		Reason:                   openAIStreamDataIntervalTimeoutReason,
		NextAccountAction:        NextAccountRetry,
	}
}
