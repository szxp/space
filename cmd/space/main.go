package main

import (
	"github.com/szxp/space"
	"github.com/szxp/space/imagemagick"

	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hashicorp/go-hclog"
)

// version will be set while building
var version string

// buildTime will be set while building
var buildTime string

const (
	envHTTPAddr     = "SPACE_HTTP_ADDR"
	envSourceDir    = "SPACE_SOURCE_DIR"
	envThumbnailDir = "SPACE_THUMBNAIL_DIR"
	envAllowedExts  = "SPACE_ALLOWED_EXTS"
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

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	sourceDir := getenv(envSourceDir, filepath.Join(home, ".space/source"))
	logger.Info("Source dir", "path", sourceDir)

	thumbnailDir := getenv(envThumbnailDir, filepath.Join(home, ".space/thumbnail"))
	logger.Info("Thumbnail dir", "path", thumbnailDir)

	allowedExts := strings.Split(getenv(envAllowedExts, ".jpeg,.png,.gif,.heif"), ",")
	logger.Info("Allowed exts", "exts", allowedExts)

	imVer, err := imagemagick.Version()
	if err != nil {
		return fmt.Errorf("Failed to check Imagemagick version: %w", err)
	}
	logger.Info("Imagemagick version", "version", imVer)

	handler, err := space.NewServer(space.ServerConfig{
		SourceDir:    sourceDir,
		ThumbnailDir: thumbnailDir,
		AllowedExts:  allowedExts,
		ImageResizer: &imagemagick.ImageResizer{},
		Logger:       logger.Named("HTTP server"),
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
