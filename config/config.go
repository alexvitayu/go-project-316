package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"
)

type AppConfig struct {
	APPEnv     string
	DefaultOps DefaultOps
}

type DefaultOps struct {
	Depth   int
	Retries int
	Delay   time.Duration
	Timeout time.Duration
	PRS     int
	Workers int
}

func LoadCfg() (*AppConfig, error) {
	env := os.Getenv("APP_ENV")

	// Загружаем env файл для конкретной среды
	if env == "development" {
		if _, err := os.Stat(".env.development"); err == nil {
			if err := godotenv.Load(".env.development"); err != nil {
				return nil, fmt.Errorf("loading %s: %w", ".env.development", err)
			}
		}
	}

	defaults := map[string]string{
		"DEPTH":   "10",
		"RETRIES": "1",
		"DELAY":   "0s",
		"TIMEOUT": "15s",
		"RPS":     "0",
		"WORKERS": "4",
	}

	intEnv := []string{"DEPTH", "RETRIES", "RPS", "WORKERS"}
	ds := make([]int, len(intEnv))
	for i, v := range intEnv {
		str := os.Getenv(v)
		if str == "" {
			str = defaults[v]
		}
		d, err := strconv.Atoi(str)
		if err != nil {
			return nil, fmt.Errorf("failed to convert string to int: %w", err)
		}
		ds[i] = d
	}

	// ParseDuration supports the next formats: ns, us, ms, s, m, h
	timeEnv := []string{"DELAY", "TIMEOUT"}
	ts := make([]time.Duration, len(timeEnv))
	for i, t := range timeEnv {
		str := os.Getenv(t)
		if str == "" {
			str = defaults[t]
		}
		duration, err := time.ParseDuration(str)
		if err != nil {
			return nil, fmt.Errorf("failed to convert string to time.Duration: %w", err)
		}
		ts[i] = duration
	}

	config := &AppConfig{
		APPEnv: env,
		DefaultOps: DefaultOps{
			Depth:   ds[0],
			Retries: ds[1],
			Delay:   ts[0],
			Timeout: ts[1],
			PRS:     ds[2],
			Workers: ds[3],
		},
	}
	return config, nil
}

// Substitute puts instead of types of flags word "value" in help-inquiry
func Substitute() {
	// Кастомизация отображения флагов
	cli.FlagStringer = func(f cli.Flag) string {
		switch flag := f.(type) {
		case *cli.IntFlag:
			return fmt.Sprintf("   --%s value\t%s (default: %v)", flag.Name, flag.Usage, flag.Value)

		case *cli.DurationFlag:
			return fmt.Sprintf("   --%s value\t%s (default: %v)", flag.Name, flag.Usage, flag.Value)

		case *cli.StringFlag:
			if flag.Value == "" {
				return fmt.Sprintf("   --%s value\t%s", flag.Name, flag.Usage)
			}
			return fmt.Sprintf("   --%s value\t%s (default: %q)", flag.Name, flag.Usage, flag.Value)
		default:
			return fmt.Sprintf("   --%s, %s\t%s", "help", "-h", "show help")
		}
	}
}
