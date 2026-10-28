package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yernsun/network-service-composer/internal/agent"
	"github.com/yernsun/network-service-composer/internal/config"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("gateway agent stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	set := flag.NewFlagSet("nsc-agent", flag.ContinueOnError)
	configPath := set.String("config", "/etc/nsc/config.yaml", "configuration file")
	showVersion := set.Bool("version", false, "print version")
	healthcheck := set.Bool("healthcheck", false, "check the running Gateway without modifying state")
	if err := set.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	cfg, err := config.LoadAgent(*configPath)
	if err != nil {
		return err
	}
	if *healthcheck {
		return agent.Healthcheck(context.Background(), cfg)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return agent.New(cfg, version, logger).Run(ctx)
}
