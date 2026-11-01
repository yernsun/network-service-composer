package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
)

func bootstrapWorkerCommand(args []string) error {
	return bootstrapWorkerWithRunner(args, maintenance.RunWorker)
}
func bootstrapWorkerWithRunner(args []string, run func(context.Context, string, string, string, string, string) error) error {
	flags := flag.NewFlagSet("bootstrap-worker", flag.ContinueOnError)
	directory := flags.String("state-dir", "", "original metadata directory")
	job := flags.String("job", "", "acknowledged system job")
	service := flags.String("service-id", "", "recorded executor service")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *directory == "" || *job == "" || *service == "" {
		return fmt.Errorf("an acknowledged maintenance job, service and original metadata directory are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return run(ctx, *directory, *job, *service, os.Getenv("NSC_MAINTENANCE_TASK_ID"), "/run/secrets/nsc-maintenance")
}
