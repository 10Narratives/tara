package agentapp

import (
	"context"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type App struct {
	cfg *Config
	log *zap.Logger
}

func NewApp(cfg *Config, log *zap.Logger) (*App, error) {
	return &App{
		cfg: cfg,
		log: log,
	}, nil
}

func (a *App) Startup(ctx context.Context) error {
	errGroup, ctx := errgroup.WithContext(ctx)
	return errGroup.Wait()
}

func (a *App) Shutdown(ctx context.Context) error {
	errGroup, ctx := errgroup.WithContext(ctx)
	return errGroup.Wait()
}
