package main

import (
	"github.com/szxp/space"

	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/hashicorp/go-hclog"
)

// version will be set while building
var version string

// buildTime will be set while building
var buildTime string

const (
	envHTTPAddr = "SPACE_HTTP_ADDR"
)

func main() {
	logger := hclog.New(&hclog.LoggerOptions{
		Output:          os.Stdout,
		Level:           hclog.LevelFromString("DEBUG"),
		IncludeLocation: true,
	}).With("appVersion", version)

	logger.Info("Build info", "time", buildTime)

	err := initialize(logger)
	if err != nil {
		logger.Error("Failed to initialize. Exit now", "err", err)
		os.Exit(1)
	}
	logger.Info("Exit normally")
}

func initialize(logger hclog.Logger) error {
	httpAddr := getenv(envHTTPAddr, ":7664")

	handler, err := space.NewServer(space.ServerConfig{
		Logger: logger.Named("HTTP server"),
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    httpAddr,
		Handler: handler,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigChan
		logger.Info("Signal received", "sig", sig)

		if err := srv.Shutdown(context.Background()); err != nil {
			logger.Error("HTTP server Shutdown", "error", err)
		}
		close(idleConnsClosed)
	}()

	err = srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	<-idleConnsClosed
	return nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}
	return value
}
