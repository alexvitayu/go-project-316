package limiter

import (
	"code/internal/models"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

func FindOutRPS(opts *models.Options) float64 {
	var rps float64
	switch {
	case opts.RPS != 0 && opts.Delay != 0:
		rps = float64(opts.RPS)

	case opts.RPS == 0 && opts.Delay != 0:
		str := opts.Delay.String()
		if strings.HasSuffix(str, "ms") {
			rps = float64(1000*time.Millisecond) / float64(opts.Delay)
		} else if strings.HasSuffix(str, "s") {
			rps = float64(time.Second) / float64(opts.Delay)
		}

	case opts.Delay == 0 && opts.RPS != 0:
		rps = float64(opts.RPS)
	default:
		rps = 0
	}
	return rps
}

func SetUpLimit(rps float64) rate.Limit {
	var limit rate.Limit
	if rps <= 0 {
		limit = rate.Inf // количество запросов за секунду не ограничено
	} else if rps > 0 {
		limit = rate.Limit(rps)
	}
	return limit
}
