package main

import (
	"context"
	"flag"
	"os/signal"
	"syscall"

	gatewayapp "github.com/10Narratives/faas/internal/app/gateway"
	configutils "github.com/10Narratives/faas/pkg/config"
	errorutils "github.com/10Narratives/faas/pkg/errors"
	logutils "github.com/10Narratives/faas/pkg/logging"
)

func main() {
	path := flag.String("config", "", "path to configuration file")
	env := flag.String("env", "", "launch environment")

	flag.Parse()

	cfg := errorutils.Must(readConfig(*path))
	log := errorutils.Must(logutils.NewLogger(*env))

	app := errorutils.Must(gatewayapp.NewApp(cfg, log))

	log.Info("starting faas-gateway application")
	startupContext, startupCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	errorutils.Try(app.Startup(startupContext))

	<-startupContext.Done()
	startupCancel()

	log.Info("stopping faas-gateway application")
	shutdownContext, shutdownCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	errorutils.Try(app.Shutdown(shutdownContext))

	shutdownCancel()
}

func readConfig(path string) (*gatewayapp.Config, error) {
	if path == "" {
		return configutils.ReadFromEnv[gatewayapp.Config]()
	}
	return configutils.ReadFromFile[gatewayapp.Config](path)
}
