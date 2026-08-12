package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAINativeStreamDataIntervalTimeoutBeforeSemanticOutputIsRequestScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{
		cfg:              &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1, MaxLineSize: defaultMaxLineSize}},
		rateLimitService: NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil),
	}
	pr, pw := io.Pipe()
	body := &firstOutputCloseTrackingBody{ReadCloser: pr, closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"idle-native"}}, Body: body}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 6101, Name: "idle-native", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
	_ = pw.Close()

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusGatewayTimeout, failoverErr.StatusCode)
	require.Equal(t, GatewayFailureScopeRequest, failoverErr.Scope)
	require.Equal(t, openAIStreamDataIntervalTimeoutReason, failoverErr.Reason)
	require.Equal(t, NextAccountRetry, failoverErr.NextAccountAction)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.False(t, failoverErr.ShouldReportAccountScheduleFailure())
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.6-sol"))
	require.Empty(t, rec.Body.String())
	select {
	case <-body.closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("native stream interval timeout did not close upstream response body")
	}
}

func TestOpenAIPassthroughStreamDataIntervalTimeoutBeforeSemanticOutputIsRequestScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{
		cfg:              &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1, MaxLineSize: defaultMaxLineSize}},
		rateLimitService: NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil),
	}
	pr, pw := io.Pipe()
	body := &firstOutputCloseTrackingBody{ReadCloser: pr, closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"idle-passthrough"}}, Body: body}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 6102, Name: "idle-passthrough", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
	_ = pw.Close()

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusGatewayTimeout, failoverErr.StatusCode)
	require.Equal(t, GatewayFailureScopeRequest, failoverErr.Scope)
	require.Equal(t, openAIStreamDataIntervalTimeoutReason, failoverErr.Reason)
	require.Equal(t, NextAccountRetry, failoverErr.NextAccountAction)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.False(t, failoverErr.ShouldReportAccountScheduleFailure())
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.6-sol"))
	require.Empty(t, rec.Body.String())
	select {
	case <-body.closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("passthrough stream interval timeout did not close upstream response body")
	}
}
