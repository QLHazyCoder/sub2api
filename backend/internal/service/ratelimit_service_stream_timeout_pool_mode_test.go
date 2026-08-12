//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type streamTimeoutCounterRecorder struct {
	count          int64
	incrementCalls int
	resetCalls     int
}

func (c *streamTimeoutCounterRecorder) IncrementTimeoutCount(_ context.Context, _ int64, _ int) (int64, error) {
	c.incrementCalls++
	return c.count, nil
}

func (c *streamTimeoutCounterRecorder) GetTimeoutCount(_ context.Context, _ int64) (int64, error) {
	return c.count, nil
}

func (c *streamTimeoutCounterRecorder) ResetTimeoutCount(_ context.Context, _ int64) error {
	c.resetCalls++
	return nil
}

func (c *streamTimeoutCounterRecorder) GetTimeoutCountTTL(_ context.Context, _ int64) (time.Duration, error) {
	return 0, nil
}

var _ TimeoutCounterCache = (*streamTimeoutCounterRecorder)(nil)

func newStreamTimeoutSettingsService(action string) (*SettingService, *runtimeSettingRepoStub) {
	repo := newRuntimeSettingRepoStub()
	repo.values[SettingKeyStreamTimeoutSettings] = `{"enabled":true,"action":"` + action + `","temp_unsched_minutes":5,"threshold_count":1,"threshold_window_minutes":1}`
	return NewSettingService(repo, &config.Config{}), repo
}

func TestRateLimitService_HandleStreamTimeout_PoolModeSkipsAccountState(t *testing.T) {
	for _, action := range []string{StreamTimeoutActionTempUnsched, StreamTimeoutActionError} {
		t.Run(action, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			counter := &streamTimeoutCounterRecorder{count: 1}
			blocker := &runtimeBlockRecorder{}
			settingService, settingRepo := newStreamTimeoutSettingsService(action)
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc.SetSettingService(settingService)
			svc.SetTimeoutCounterCache(counter)
			svc.SetAccountRuntimeBlocker(blocker)
			account := &Account{
				ID:       808,
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"pool_mode": true,
				},
			}

			for i := 0; i < 2; i++ {
				require.False(t, svc.HandleStreamTimeout(context.Background(), account, "gpt-5.6-sol"))
			}

			require.Zero(t, settingRepo.getValueCalls)
			require.Zero(t, counter.incrementCalls)
			require.Zero(t, counter.resetCalls)
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.setErrorCalls)
			require.Empty(t, blocker.accounts)
		})
	}
}

func TestRateLimitService_HandleStreamTimeout_NonPoolModeStillAppliesConfiguredAction(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &streamTimeoutCounterRecorder{count: 1}
	blocker := &runtimeBlockRecorder{}
	settingService, _ := newStreamTimeoutSettingsService(StreamTimeoutActionTempUnsched)
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(settingService)
	svc.SetTimeoutCounterCache(counter)
	svc.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 809, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	require.True(t, svc.HandleStreamTimeout(context.Background(), account, "gpt-5.6-sol"))
	require.Equal(t, 1, counter.incrementCalls)
	require.Equal(t, 1, counter.resetCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Zero(t, repo.setErrorCalls)
	require.Len(t, blocker.accounts, 1)
}
