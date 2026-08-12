// Command btassistantbyr is the executable entry point of BTAssistantByr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/app"
	"github.com/Yokumii/BTAssistantByr/internal/config"
	"github.com/Yokumii/BTAssistantByr/internal/logging"
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
)

const defaultConfigPath = "config/config.yaml"

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "BTAssistantByr 启动失败：%v\n", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
	output io.Writer,
	errorOutput io.Writer,
) (resultErr error) {
	flags := flag.NewFlagSet("btassistantbyr", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	configPath := flags.String("config", defaultConfigPath, "YAML configuration file path")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if *showVersion {
		if _, err := fmt.Fprintln(output, version); err != nil {
			return fmt.Errorf("write version: %w", err)
		}
		return nil
	}

	configuration, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logSystem, err := logging.New(logging.Options{
		Level:   configuration.Runtime.LogLevel,
		Color:   configuration.Runtime.LogColor,
		LogDir:  configuration.Runtime.LogDir,
		Console: errorOutput,
	})
	if err != nil {
		return fmt.Errorf("initialize logging: %w", err)
	}
	defer func() {
		if err := logSystem.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close logging: %w", err))
		}
	}()
	logger := logSystem.Logger()
	logger.InfoContext(ctx, "initializing BTAssistantByr",
		"version", version,
		"execution_mode", configuration.Execution.Mode,
		"log_level", configuration.Runtime.LogLevel,
		"log_file", logSystem.FilePath(),
	)
	application, err := assembleUntilReachable(ctx, configuration.Strategy.RefreshInterval, logger, func() (*app.Application, error) {
		return app.AssembleWithLogger(ctx, configuration, input, output, logger)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			logger.Info("BTAssistantByr stopped before initialization completed")
			return nil
		}
		logger.ErrorContext(ctx, "application assembly failed", "error", err)
		return fmt.Errorf("assemble application: %w", err)
	}
	if err := application.Run(ctx); err != nil {
		return fmt.Errorf("run application: %w", err)
	}
	return nil
}

func assembleUntilReachable[T any](
	ctx context.Context,
	retryInterval time.Duration,
	logger *slog.Logger,
	assemble func() (T, error),
) (T, error) {
	var zero T
	for {
		result, err := assemble()
		if err == nil {
			return result, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, ctxErr
		}
		if !netretry.IsTransient(err) {
			return zero, err
		}
		logger.WarnContext(ctx, "application initialization skipped after network retries",
			"next_attempt_in", retryInterval,
			"error", err,
		)

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}
