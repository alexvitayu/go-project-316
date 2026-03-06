package main

import (
	"code/internal/code/crawler"
	"code/internal/config"
	"code/internal/logger"
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/urfave/cli/v3"
)

func main() {
	ctx := context.Background()

	cfg, err := config.LoadCfg()
	if err != nil {
		slog.Error("LoadConfig", "error", err)
	}

	customLogger := logger.SetupLogger(cfg)
	slog.SetDefault(customLogger)

	slog.Info("Application started")
	slog.Debug(fmt.Sprintf("APP_ENV=%s", cfg.APPEnv))

	cmd := &cli.Command{

		Name:            "hexlet-go-crawler",
		Usage:           "analyze a website structure",
		UsageText:       "hexlet-go-crawler [global options] command [command options] <url>",
		HideHelpCommand: true,

		Commands: []*cli.Command{
			{
				Name:    "helр",
				Aliases: []string{"h"},
				Usage:   "Shows a list of commands or help for one command",
			},
		},

		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "depth",
				Value: cfg.DefaultOps.Depth,
				Usage: "crawl depth",
			},
			&cli.IntFlag{
				Name:  "retries",
				Value: cfg.DefaultOps.Retries,
				Usage: "number of retries for failed requests"},
			&cli.DurationFlag{
				Name:  "delay",
				Value: cfg.DefaultOps.Delay,
				Usage: "delay between requests (example: 200ms, 1s)"},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: cfg.DefaultOps.Timeout,
				Usage: "per-request timeout"},
			&cli.IntFlag{
				Name:  "rps",
				Value: cfg.DefaultOps.PRS,
				Usage: "limit requests per second (overrides delay)"},
			&cli.StringFlag{
				Name:  "user-agent",
				Usage: "custom user agent"},
			&cli.IntFlag{
				Name:  "workers",
				Value: cfg.DefaultOps.Workers,
				Usage: "number of concurrent workers"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			var url string
			if cmd.Args().Len() > 0 {
				url = cmd.Args().Get(0)
			}
			depth := cmd.Int("depth")
			retries := cmd.Int("retries")
			delay := cmd.Duration("delay")
			timeout := cmd.Duration("timeout")
			rps := cmd.Int("rps")
			agent := cmd.String("user-agent")
			workers := cmd.Int("workers")

			client := http.Client{
				Timeout: 5 * time.Second,
			}

			options := crawler.Options{
				URL:         url,
				Depth:       depth,
				Retries:     retries,
				Delay:       delay,
				Timeout:     timeout,
				UserAgent:   agent,
				Concurrency: workers,
				IndentJSON:  "  ",
				HTTPClient:  &client,
				RPS:         rps,
			}

			report, err := crawler.Analyze(ctx, options)
			if err != nil {
				fmt.Println(err) //TODO
			}

			fmt.Println(string(report))
			fmt.Println(options)
			return nil
		},
	}

	config.Substitute()

	if err := cmd.Run(ctx, os.Args); err != nil {
		log.Fatal(err) //TODO
	}
	slog.Info("See you!")
}
