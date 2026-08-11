package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/adapter/byr"
	"github.com/Yokumii/BTAssistantByr/internal/adapter/qbittorrent"
	"github.com/Yokumii/BTAssistantByr/internal/config"
	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/service/topfree"
)

// Application owns the assembled TopFree polling lifecycle.
type Application struct {
	poller  *topfree.Poller
	handler topfree.Handler
	logger  *slog.Logger
}

// Assemble authenticates external clients and connects the complete TopFree pipeline.
func Assemble(
	ctx context.Context,
	configuration config.Config,
	approvalInput io.Reader,
	approvalOutput io.Writer,
) (*Application, error) {
	return AssembleWithLogger(ctx, configuration, approvalInput, approvalOutput, nil)
}

// AssembleWithLogger authenticates and assembles the pipeline with structured logs.
func AssembleWithLogger(
	ctx context.Context,
	configuration config.Config,
	approvalInput io.Reader,
	approvalOutput io.Writer,
	logger *slog.Logger,
) (*Application, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rootLogger := usableLogger(logger)
	logger = rootLogger.With("component", "app")
	logger.DebugContext(ctx, "assembling application",
		"tracker_base_url", configuration.Tracker.BaseURL,
		"tracker_proxy_enabled", configuration.Tracker.ProxyURL != "",
		"tracker_cookie_configured", configuration.Tracker.Cookie != "",
		"qbit_base_url", configuration.Client.BaseURL,
		"save_path", configuration.Client.SavePath,
		"execution_mode", configuration.Execution.Mode,
	)
	approver, err := executionApprover(configuration.Execution.Mode, approvalInput, approvalOutput)
	if err != nil {
		return nil, err
	}

	byrAuthenticator, err := byr.NewAuthenticatorWithProxy(
		configuration.Tracker.BaseURL,
		configuration.Tracker.ProxyURL,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create BYR authenticator: %w", err)
	}
	logger.DebugContext(ctx, "BYR authenticator created", "proxy_enabled", configuration.Tracker.ProxyURL != "")
	byrStarted := time.Now()
	logger.DebugContext(ctx, "establishing BYR session", "reuse_auth_token", configuration.Tracker.Cookie != "")
	byrCredential, err := authenticateBYR(ctx, byrAuthenticator, configuration.Tracker)
	if err != nil {
		return nil, err
	}
	logger.InfoContext(ctx, "BYR session ready", "reused_auth_token", configuration.Tracker.Cookie != "", "duration", time.Since(byrStarted))
	byrClient, err := byrAuthenticator.NewClient(byrCredential)
	if err != nil {
		return nil, fmt.Errorf("create BYR client: %w", err)
	}

	qbitAuthenticator, err := qbittorrent.NewAuthenticator(configuration.Client.BaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create qBittorrent authenticator: %w", err)
	}
	logger.DebugContext(ctx, "qBittorrent authenticator created")
	qbitStarted := time.Now()
	logger.DebugContext(ctx, "authenticating qBittorrent")
	qbitCredential, err := qbitAuthenticator.Login(
		ctx,
		configuration.Client.Username,
		configuration.Client.Password,
	)
	if err != nil {
		return nil, fmt.Errorf("authenticate qBittorrent: %w", err)
	}
	logger.InfoContext(ctx, "qBittorrent session ready", "cookie_name", qbitCredential.CookieName, "duration", time.Since(qbitStarted))
	qbitClient, err := qbitAuthenticator.NewClient(qbitCredential)
	if err != nil {
		return nil, fmt.Errorf("create qBittorrent client: %w", err)
	}

	executor, err := topfree.NewExecutor(byrClient, qbitClient, configuration.Client.SavePath, configuration.Strategy.MinFreeSpaceBytes)
	if err != nil {
		return nil, fmt.Errorf("create TopFree executor: %w", err)
	}
	executor.SetLogger(rootLogger.With("component", "topfree_executor"))
	guardedExecutor, err := topfree.NewPolicyExecutor(configuration.Execution.Mode, executor, approver)
	if err != nil {
		return nil, fmt.Errorf("create TopFree execution policy: %w", err)
	}
	guardedExecutor.SetLogger(rootLogger.With("component", "execution_policy"))
	coordinator, err := topfree.NewCoordinator(
		qbitClient,
		guardedExecutor,
		configuration.Strategy.MaxDiskUsageBytes,
		configuration.Strategy.MaxTorrentSizeBytes,
		configuration.Strategy.MinFreeSpaceBytes,
		configuration.Client.SavePath,
	)
	if err != nil {
		return nil, fmt.Errorf("create TopFree coordinator: %w", err)
	}
	coordinator.SetLogger(rootLogger.With("component", "topfree_coordinator"))
	poller, err := topfree.NewPoller(byrClient, configuration.Strategy.RefreshInterval)
	if err != nil {
		return nil, fmt.Errorf("create TopFree poller: %w", err)
	}
	poller.SetLogger(rootLogger.With("component", "topfree_poller"))
	logger.DebugContext(ctx, "application assembly completed")

	return &Application{poller: poller, handler: coordinator.Handle, logger: logger}, nil
}

// Run polls immediately and then at the configured interval until failure or cancellation.
func (application *Application) Run(ctx context.Context) error {
	application.logger.InfoContext(ctx, "BTAssistantByr started; press Ctrl+C to stop")
	err := application.poller.Run(ctx, application.handler)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		application.logger.Info("BTAssistantByr stopped")
		return nil
	}
	if errors.Is(err, topfree.ErrExecutionDeclined) {
		application.logger.WarnContext(ctx, "BTAssistantByr stopped because writes were not approved")
	} else if err != nil {
		application.logger.ErrorContext(ctx, "BTAssistantByr stopped with an error", "error", err)
	}
	return err
}

func usableLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func executionApprover(
	mode domain.ExecutionMode,
	input io.Reader,
	output io.Writer,
) (topfree.Approver, error) {
	switch mode {
	case domain.ExecutionModeYOLO:
		return nil, nil
	case domain.ExecutionModeRequestApproval:
		approver, err := topfree.NewTerminalApprover(input, output)
		if err != nil {
			return nil, fmt.Errorf("create terminal approver: %w", err)
		}
		return approver, nil
	default:
		return nil, fmt.Errorf("%w: %q", topfree.ErrInvalidExecutionMode, mode)
	}
}

func authenticateBYR(
	ctx context.Context,
	authenticator *byr.Authenticator,
	configuration config.Tracker,
) (byr.Credential, error) {
	if configuration.Cookie != "" {
		credential, err := byr.NewCredential(configuration.Cookie)
		if err != nil {
			return byr.Credential{}, fmt.Errorf("use configured BYR auth token: %w", err)
		}
		return credential, nil
	}

	credential, err := authenticator.Login(ctx, configuration.Username, configuration.Password)
	if err != nil {
		return byr.Credential{}, fmt.Errorf("authenticate BYR: %w", err)
	}
	return credential, nil
}
