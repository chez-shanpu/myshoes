package runner

import (
	"context"
	"time"

	"github.com/whywaita/myshoes/pkg/datastore"
	"github.com/whywaita/myshoes/pkg/logger"
)

var (
	// GoalCheckerInterval is interval time of check deleting runner
	GoalCheckerInterval = 1 * time.Minute
	// MustGoalTime is hard limit for idle runner.
	// So it is same as the limit of GitHub Actions
	MustGoalTime = 6 * time.Hour
	// MustRunningTime is set time of instance create + download binaries + etc
	MustRunningTime = 5 * time.Minute
	// TargetTokenInterval is interval time of checking target token
	TargetTokenInterval = 5 * time.Minute
	//NeedRefreshToken is time of token expired
	NeedRefreshToken = 10 * time.Minute
)

// Manager is runner management
type Manager struct {
	ds datastore.Datastore
}

// New create a Manager
func New(ds datastore.Datastore) *Manager {
	return &Manager{
		ds: ds,
	}
}

// Loop check
func (m *Manager) Loop(ctx context.Context) error {
	logger.Logf(false, "start runner loop")

	ticker := time.NewTicker(GoalCheckerInterval)
	defer ticker.Stop()

	if err := m.doTargetToken(ctx); err != nil {
		logger.Logf(false, "failed to refresh token in initialize: %+v", err)
	}

	go func(ctx context.Context) {
		tokenRefreshTicker := time.NewTicker(TargetTokenInterval)
		defer tokenRefreshTicker.Stop()

		for {
			select {
			case <-tokenRefreshTicker.C:
				if err := m.doTargetToken(ctx); err != nil {
					logger.Logf(false, "failed to refresh token: %+v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}(ctx)

	for {
		select {
		case <-ticker.C:
			if err := m.do(ctx); err != nil {
				logger.Logf(false, "failed to starter: %+v", err)
			}
		case <-ctx.Done():
			return nil
		}
	}
}
