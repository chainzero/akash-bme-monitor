package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/chainzero/akash-bme-monitor/internal/alerting"
	"github.com/chainzero/akash-bme-monitor/internal/announcements"
	"github.com/chainzero/akash-bme-monitor/internal/bme"
	"github.com/chainzero/akash-bme-monitor/internal/config"
	"github.com/chainzero/akash-bme-monitor/internal/guardian"
	"github.com/chainzero/akash-bme-monitor/internal/hermes"
	"github.com/chainzero/akash-bme-monitor/internal/oracle"
	"github.com/chainzero/akash-bme-monitor/internal/report"
)

func main() {
	// test-vaa subcommand: exercises VAA fetch logic without a live blockchain.
	// Usage: price-feed-monitor test-vaa --target-index <N>
	if len(os.Args) > 1 && os.Args[1] == "test-vaa" {
		fs := flag.NewFlagSet("test-vaa", flag.ExitOnError)
		targetIndex := fs.Uint("target-index", 0, "guardian set index to fetch the upgrade VAA for (required)")
		fs.Parse(os.Args[2:]) //nolint:errcheck // ExitOnError handles this
		if *targetIndex == 0 {
			fmt.Fprintln(os.Stderr, "usage: price-feed-monitor test-vaa --target-index <N>")
			os.Exit(1)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		os.Exit(guardian.RunVAAFetchTest(ctx, os.Getenv("ETHERSCAN_API_KEY"), uint32(*targetIndex), os.Stdout))
	}

	defaultConfig := "config.yaml"
	if v := os.Getenv(config.EnvConfigPath); v != "" {
		defaultConfig = v
	}
	configPath := flag.String("config", defaultConfig, "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var alerter alerting.Alerter = alerting.NewSlack(cfg.Slack.WebhookURL)

	// Component 1: Oracle Price Health
	if cfg.OraclePriceMonitor.Enabled {
		for _, network := range cfg.Networks {
			pm := oracle.NewPriceMonitor(network, cfg.OraclePriceMonitor, alerter, logger)
			go pm.Run(ctx)
		}
		slog.Info("oracle price monitor enabled", "networks", len(cfg.Networks))
	}

	// Component 2: Hermes Relayer Health
	if cfg.HermesHealthMonitor.Enabled {
		for _, network := range cfg.Networks {
			hm := hermes.NewHealthMonitor(network, cfg.HermesHealthMonitor, alerter, logger)
			go hm.Run(ctx)
		}
		slog.Info("hermes health monitor enabled", "networks", len(cfg.Networks))
	}

	// Component 3: Guardian Set Currency
	if cfg.GuardianSetMonitor.Enabled {
		gm := guardian.NewSyncMonitor(cfg.GuardianSetMonitor, cfg.Networks, alerter, logger)
		go gm.Run(ctx)
		slog.Info("guardian set monitor enabled")
	}

	// Component 6: BME Status Monitor
	if cfg.BMEMonitor.Enabled {
		for _, network := range cfg.Networks {
			bm := bme.NewStatusMonitor(network, cfg.BMEMonitor, alerter, logger)
			go bm.Run(ctx)
		}
		slog.Info("BME status monitor enabled", "networks", len(cfg.Networks))
	}

	// Component 5: Wormholescan Guardian Set Monitor
	if cfg.WormholescanMonitor.Enabled {
		wm := guardian.NewWormholescanMonitor(cfg.WormholescanMonitor, cfg.Networks, cfg.GuardianSetMonitor.EtherscanAPIKey, alerter, logger)
		go wm.Run(ctx)
		slog.Info("wormholescan guardian set monitor enabled")
	}

	// Component 4: Guardian Update Announcements
	if cfg.AnnouncementMonitor.Enabled {
		if cfg.AnnouncementMonitor.PythForum.Enabled {
			fm := announcements.NewPythForumMonitor(cfg.AnnouncementMonitor.PythForum, alerter, logger)
			go fm.Run(ctx)
			slog.Info("pyth forum monitor enabled")
		}
		if cfg.AnnouncementMonitor.GitHub.Enabled {
			gm := announcements.NewGitHubGuardianMonitor(cfg.AnnouncementMonitor.GitHub, alerter, logger)
			go gm.Run(ctx)
			slog.Info("github guardian monitor enabled", "repo", cfg.AnnouncementMonitor.GitHub.Repo)
		}
	}

	// Startup summary + daily health check
	reporter := report.New(cfg, alerter, logger)
	reporter.PostStartup(ctx)
	go reporter.RunDailySchedule(ctx)

	slog.Info("price-feed-monitor started")
	<-ctx.Done()
	slog.Info("shutting down")
}

