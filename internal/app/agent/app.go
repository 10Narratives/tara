package agentapp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	runtime "github.com/10Narratives/faas/internal/runtime"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type App struct {
	cfg     *Config
	log     *zap.Logger
	manager *runtime.Manager
}

func NewApp(cfg *Config, log *zap.Logger) (*App, error) {
	managerCfg := &runtime.ManagerConfig{
		MaxInstances:     10,
		InstanceLifetime: 5 * time.Minute,
		ColdStart:        2 * time.Second,
		NATSURL:          cfg.UnifiedStorage.URL,
		PodName:          os.Getenv("POD_NAME"), // Docker ENV
		MaxAckPending:    2,
		AckWait:          10 * time.Minute,
		MaxDeliver:       5,
		Backoff:          []time.Duration{30 * time.Second, 1 * time.Minute, 2 * time.Minute},
	}
	manager, err := runtime.NewManager(log, managerCfg)
	if err != nil {
		return nil, fmt.Errorf("create runtime manager: %w", err)
	}

	return &App{
		cfg:     cfg,
		log:     log,
		manager: manager,
	}, nil
}

func (a *App) Startup(ctx context.Context) error {
	errGroup, ctx := errgroup.WithContext(ctx)

	errGroup.Go(func() error {
		http.Handle("/metrics", promhttp.Handler())
		return http.ListenAndServe(":8080", nil)
	})

	errGroup.Go(func() error {
		a.log.Info("start function manager")
		return a.manager.Run(ctx)
	})

	return errGroup.Wait()
}

func (a *App) Shutdown(ctx context.Context) error {
	errGroup, ctx := errgroup.WithContext(ctx)

	errGroup.Go(func() error {
		if a.manager != nil {
			if err := a.manager.Stop(ctx); err != nil {
				a.log.Warn("manager stop failed", zap.Error(err))
			}
		}
		return nil
	})

	return errGroup.Wait()
}
