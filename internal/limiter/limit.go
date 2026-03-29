package limiter

import (
	"strings"
	"time"

	"golang.org/x/time/rate"
)

func FindOutRPS(RPS int, delay time.Duration) float64 {
	var rps float64
	switch {
	case RPS != 0 && delay != 0:
		rps = float64(RPS)

	case RPS == 0 && delay != 0:
		str := delay.String()
		if strings.HasSuffix(str, "ms") {
			rps = float64(1000*time.Millisecond) / float64(delay)
		} else if strings.HasSuffix(str, "s") {
			rps = float64(time.Second) / float64(delay)
		}

	case delay == 0 && RPS != 0:
		rps = float64(RPS)
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
