package agentapp

import (
	"context"
	"fmt"
	"time"

	natscomp "github.com/10Narratives/faas/internal/app/components/nats"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type App struct {
	cfg            *Config
	log            *zap.Logger
	unifiedStorage *nats.Conn
}

func NewApp(cfg *Config, log *zap.Logger) (*App, error) {
	unifiedStorage, err := natscomp.NewConnection(cfg.UnifiedStorage.URL)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to unified storage: %w", err)
	}
	log.Info("connection to unified storage established")

	return &App{
		cfg:            cfg,
		log:            log,
		unifiedStorage: unifiedStorage,
	}, nil
}

func (a *App) Startup(ctx context.Context) error {
	errGroup, ctx := errgroup.WithContext(ctx)

	errGroup.Go(func() error {
		a.log.Info("agent online")
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		select {
		case <-ticker.C:
			a.log.Info("work done")
			return nil
		case <-ctx.Done():
			a.log.Info("work canceled")
			return nil
		}
	})

	return errGroup.Wait()
}

func (a *App) Shutdown(ctx context.Context) error {
	errGroup, ctx := errgroup.WithContext(ctx)
	return errGroup.Wait()
}
