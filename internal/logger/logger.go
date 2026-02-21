package logger

import (
	"code/internal/config"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
)

func SetupLogger(cfg *config.AppConfig) *slog.Logger {
	var level slog.Leveler
	var addSource bool
	var noColor bool
	if cfg.APPEnv == "development" {
		level = slog.LevelDebug
		addSource = true
		noColor = false
	} else {
		level = slog.LevelInfo
		addSource = false
		noColor = true
	}

	handler := tint.NewHandler(os.Stdout, &tint.Options{
		AddSource:  addSource,
		Level:      level,
		TimeFormat: time.RFC1123,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			_ = groups
			if a.Key == slog.LevelKey {
				// Преобразуем уровень в верхний регистр
				l := a.Value.String()
				return slog.String("level", strings.ToUpper(l))
			}
			return a
		},
		NoColor: noColor,
	})
	logger := slog.New(handler)
	return logger
}
