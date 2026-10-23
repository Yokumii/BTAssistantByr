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
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
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
	retryPolicy := netretry.Policy{
		Retries: configuration.Runtime.NetworkRetryCount,
		Delay:   time.Second,
		Logger:  rootLogger.With("component", "network_retry"),
	}
	logger.DebugContext(ctx, "assembling application",
		"tracker_base_url", configuration.Tracker.BaseURL,
		"tracker_proxy_enabled", configuration.Tracker.ProxyURL != "",
		"tracker_cookie_configured", configuration.Tracker.Cookie != "",
		"qbit_base_url", configuration.Client.BaseURL,
		"save_path", configuration.Client.SavePath,
		"execution_mode", configuration.Execution.Mode,
		"refresh_interval", configuration.Strategy.RefreshInterval,
		"queue_check_interval", configuration.Strategy.QueueCheckInterval,
		"network_retry_count", configuration.Runtime.NetworkRetryCount,
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
	byrSessionCache, err := byr.NewSessionCache(configuration.Runtime.DataDir)
	if err != nil {
		return nil, fmt.Errorf("create BYR session cache: %w", err)
	}
	byrCredential, byrCredentialSource, err := loadBYRCredential(byrSessionCache, configuration.Tracker)
	if err != nil {
		return nil, fmt.Errorf("load BYR session: %w", err)
	}
	byrStarted := time.Now()
	logger.DebugContext(ctx, "establishing BYR session", "credential_source", byrCredentialSource, "session_cache_path", byrSessionCache.Path())
	if byrCredentialSource == byrCredentialSourceLogin {
		byrCredential, err = netretry.DoValue(ctx, retryPolicy, "authenticate BYR", func() (byr.Credential, error) {
			credential, loginErr := byrAuthenticator.Login(ctx, configuration.Tracker.Username, configuration.Tracker.Password)
			if loginErr != nil {
				return byr.Credential{}, fmt.Errorf("authenticate BYR: %w", loginErr)
			}
			return credential, nil
		})
		if err != nil {
			return nil, err
		}
		if err := byrSessionCache.Save(configuration.Tracker.BaseURL, configuration.Tracker.Username, byrCredential); err != nil {
			return nil, fmt.Errorf("cache BYR session after login: %w", err)
		}
	}
	logger.InfoContext(ctx, "BYR session ready", "credential_source", byrCredentialSource, "duration", time.Since(byrStarted))
	byrSessionLogger := rootLogger.With("component", "byr_session")
	byrCredentialRefresher := &byrCredentialRefresher{
		authenticator: byrAuthenticator,
		cache:         byrSessionCache,
		configuration: configuration.Tracker,
		source:        byrCredentialSource,
		logger:        byrSessionLogger,
	}
	maxSessionRefreshes := 1
	if byrCredentialSource == byrCredentialSourceCache && configuration.Tracker.Cookie != "" {
		maxSessionRefreshes = 2
	}
	byrClient, err := byrAuthenticator.NewRefreshingClientWithInitialFallbacks(byrCredential, func(refreshContext context.Context) (byr.Credential, error) {
		return byrCredentialRefresher.Refresh(refreshContext)
	}, maxSessionRefreshes)
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
	qbitCredential, err := netretry.DoValue(ctx, retryPolicy, "authenticate qBittorrent", func() (qbittorrent.Credential, error) {
		return qbitAuthenticator.Login(
			ctx,
			configuration.Client.Username,
			configuration.Client.Password,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("authenticate qBittorrent: %w", err)
	}
	logger.InfoContext(ctx, "qBittorrent session ready", "cookie_name", qbitCredential.CookieName, "duration", time.Since(qbitStarted))
	qbitClient, err := qbitAuthenticator.NewClient(qbitCredential)
	if err != nil {
		return nil, fmt.Errorf("create qBittorrent client: %w", err)
	}
	retryingTracker := netretry.NewTracker(byrClient, retryPolicy)
	retryingTorrentClient := netretry.NewTorrentClient(qbitClient, retryPolicy)

	executor, err := topfree.NewExecutor(retryingTracker, retryingTorrentClient, configuration.Client.SavePath, configuration.Strategy.MinFreeSpaceBytes)
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
		retryingTorrentClient,
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
	poller, err := topfree.NewPoller(retryingTracker, configuration.Strategy.RefreshInterval, configuration.Strategy.QueueCheckInterval)
	if err != nil {
		return nil, fmt.Errorf("create TopFree poller: %w", err)
	}
	poller.SetLogger(rootLogger.With("component", "topfree_poller"))
	logger.DebugContext(ctx, "application assembly completed")

	return &Application{poller: poller, handler: coordinator.Handle, logger: logger}, nil
}

// Run refreshes tracker state and checks the queue until a terminal error or cancellation.
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

type byrCredentialSource string

const (
	byrCredentialSourceCache  byrCredentialSource = "cache"
	byrCredentialSourceConfig byrCredentialSource = "config_cookie"
	byrCredentialSourceLogin  byrCredentialSource = "login"
)

type byrCredentialRefresher struct {
	authenticator *byr.Authenticator
	cache         *byr.SessionCache
	configuration config.Tracker
	source        byrCredentialSource
	logger        *slog.Logger
}

func (refresher *byrCredentialRefresher) Refresh(ctx context.Context) (byr.Credential, error) {
	if refresher.source == byrCredentialSourceCache && refresher.configuration.Cookie != "" {
		refresher.logger.WarnContext(ctx, "cached BYR session expired; trying configured auth token")
		credential, err := byr.NewCredential(refresher.configuration.Cookie)
		if err != nil {
			return byr.Credential{}, fmt.Errorf("use configured BYR auth token: %w", err)
		}
		refresher.source = byrCredentialSourceConfig
		return credential, nil
	}

	started := time.Now()
	refresher.logger.WarnContext(ctx, "BYR session expired; reauthenticating")
	credential, err := refresher.authenticator.Login(
		ctx,
		refresher.configuration.Username,
		refresher.configuration.Password,
	)
	if err != nil {
		return byr.Credential{}, fmt.Errorf("reauthenticate BYR: %w", err)
	}
	if err := refresher.cache.Save(refresher.configuration.BaseURL, refresher.configuration.Username, credential); err != nil {
		return byr.Credential{}, fmt.Errorf("cache renewed BYR session: %w", err)
	}
	refresher.source = byrCredentialSourceLogin
	refresher.logger.InfoContext(ctx, "BYR session renewed", "duration", time.Since(started))
	return credential, nil
}

func loadBYRCredential(cache *byr.SessionCache, configuration config.Tracker) (byr.Credential, byrCredentialSource, error) {
	credential, found, err := cache.Load(configuration.BaseURL, configuration.Username)
	if err != nil {
		return byr.Credential{}, "", err
	}
	if found {
		return credential, byrCredentialSourceCache, nil
	}
	if configuration.Cookie != "" {
		credential, err = byr.NewCredential(configuration.Cookie)
		if err != nil {
			return byr.Credential{}, "", fmt.Errorf("use configured BYR auth token: %w", err)
		}
		return credential, byrCredentialSourceConfig, nil
	}
	return byr.Credential{}, byrCredentialSourceLogin, nil
}
