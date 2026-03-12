package logger

import (
	"code/config"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
)

func SetupLogger(cfg *config.AppConfig) *slog.Logger {
	var level slog.Leveler
	var addSource bool
	var out *os.File

	if cfg.APPEnv == "development" {
		level = slog.LevelDebug
		addSource = true
		out = os.Stdout
	} else {
		level = slog.LevelInfo
		addSource = false
		out = os.Stderr
	}

	handler := tint.NewHandler(out, &tint.Options{
		AddSource:  addSource,
		Level:      level,
		TimeFormat: time.RFC1123,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				// Преобразуем уровень в верхний регистр
				l := a.Value.String()
				return slog.String("level", strings.ToUpper(l))
			}
			return a
		},
		NoColor: true,
	})

	logger := slog.New(handler)
	return logger
}
