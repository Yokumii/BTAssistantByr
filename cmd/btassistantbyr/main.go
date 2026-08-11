// Command btassistantbyr is the executable entry point of BTAssistantByr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Yokumii/BTAssistantByr/internal/app"
	"github.com/Yokumii/BTAssistantByr/internal/config"
)

const defaultConfigPath = "config/config.yaml"

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
) error {
	flags := flag.NewFlagSet("btassistantbyr", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	configPath := flags.String("config", defaultConfigPath, "YAML configuration file path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	configuration, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if _, err := fmt.Fprintf(output, "正在初始化 BTAssistantByr，执行模式：%s\n", configuration.Execution.Mode); err != nil {
		return fmt.Errorf("write initialization status: %w", err)
	}
	application, err := app.Assemble(ctx, configuration, input, output)
	if err != nil {
		return fmt.Errorf("assemble application: %w", err)
	}
	if _, err := fmt.Fprintln(output, "BTAssistantByr 已启动，按 Ctrl+C 安全退出。"); err != nil {
		return fmt.Errorf("write startup status: %w", err)
	}
	if err := application.Run(ctx); err != nil {
		return fmt.Errorf("run application: %w", err)
	}
	if _, err := fmt.Fprintln(output, "BTAssistantByr 已停止。"); err != nil {
		return fmt.Errorf("write shutdown status: %w", err)
	}
	return nil
}
