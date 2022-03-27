package main

import (
	"github.com/szxp/space"
	"github.com/szxp/space/imagemagick"

	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/go-hclog"
)

// version will be set while building
var version string

// buildTime will be set while building
var buildTime string

func main() {
	var configPath = flag.String("f", "config.toml", "Path to configuration file")
	flag.Parse()
	conf, err := readConfig(*configPath)
	if err != nil {
		fmt.Println("Reading configuration file failed:", err)
		os.Exit(1)
	}

	logger := hclog.New(&hclog.LoggerOptions{
		Output:          os.Stdout,
		Level:           hclog.LevelFromString(conf.Log.Level),
		IncludeLocation: true,
	}).With("appVersion", version)

	logger.Info("Build info", "time", buildTime)

	err = initialize(conf, logger)
	if err != nil {
		logger.Error("Failed to initialize. Exit now", "err", err)
		os.Exit(1)
	}
	logger.Info("Exit normally")
}

func initialize(conf *config, logger hclog.Logger) error {
	imVer, err := imagemagick.Version()
	if err != nil {
		return fmt.Errorf("Failed to check Imagemagick version: %w", err)
	}
	logger.Info("Imagemagick version", "version", imVer)

	handler, err := space.NewServer(space.ServerConfig{
		SourceDir:             conf.SourceDir,
		ThumbnailDir:          conf.ThumbnailDir,
		AllowedExts:           conf.AllowedExtensions,
		ImageResizer:          &imagemagick.ImageResizer{},
		DefaultThumbnailWidth: conf.DefaultThumbnailWidth,
		AllowedThumbnailSizes: conf.AllowedThumbnailSizes,
		ThumbnailMaxAge:       conf.ThumbnailMaxAge,
		Logger:                logger.Named("httpserver"),
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    conf.HTTPServer.Address,
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

	logger.Info("Start HTTP server", "address", conf.HTTPServer.Address)
	err = srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	<-idleConnsClosed
	return nil
}

func readConfig(path string) (*config, error) {
	config := &config{}
	_, err := toml.DecodeFile(path, config)
	if err != nil {
		return nil, err
	}
	return config, nil
}

type config struct {
	SourceDir             string
	ThumbnailDir          string
	AllowedExtensions     []string
	DefaultThumbnailWidth uint64
	AllowedThumbnailSizes space.ThumbnailSizes
	ThumbnailMaxAge       int64
	HTTPServer            httpServer
	Log log
}

type httpServer struct {
	Address string
}

type log struct {
	Level string
}
