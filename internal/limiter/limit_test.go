package limiter

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestSetLimit(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name      string
		RPS       int
		Delay     time.Duration
		wantLimit float64
	}{
		{
			name:      "not_set_rps_and_delay",
			RPS:       0,
			wantLimit: 0,
		},
		{
			name:      "set_delay_millisecond",
			Delay:     250 * time.Millisecond,
			wantLimit: 4,
		},
		{
			name:      "set_delay_second",
			Delay:     4 * time.Second,
			wantLimit: 0.25,
		},
		{
			name:      "set_rps_and_delay",
			RPS:       3,
			Delay:     4 * time.Second,
			wantLimit: 3,
		},
		{
			name:      "set_rps",
			RPS:       4,
			wantLimit: 4,
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FindOutRPS(tc.RPS, tc.Delay)
			assert.InDelta(t, tc.wantLimit, got, 0.0001)
		})
	}
}

func TestLimiter_TimeBetweenRequests(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		rps          float64
		delay        time.Duration
		workers      int
		reqPerWorker int
		burst        int
	}{
		{
			name:         "Delay 200ms -> 5 RPS, 3 workers",
			rps:          0,
			delay:        200 * time.Millisecond,
			workers:      3,
			reqPerWorker: 4,
		},
		{
			name:         "RPS 3 has priority over delay 100ms",
			rps:          3,
			delay:        100 * time.Millisecond,
			workers:      2,
			reqPerWorker: 5,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			limiter := rate.NewLimiter(SetUpLimit(tc.rps), tc.workers)

			var wg sync.WaitGroup
			requestTimes := make([][]time.Time, tc.workers) // вместит в себя сумму всех времён запросов от всех воркеров

			start := time.Now()

			for w := range tc.workers {
				wg.Add(1)
				go func(workerID int) {
					defer wg.Done()
					times := make([]time.Time, 0, tc.reqPerWorker)

					for range tc.reqPerWorker {
						err := limiter.Wait(t.Context()) // здесь лимитер создаёт определённую задержку между запросами
						require.NoError(t, err)
						times = append(times, time.Now()) // сюда складываются текущие времена выполнения запросов
					}

					requestTimes[workerID] = times
				}(w)
			}

			wg.Wait()

			// Собираем и сортируем все времена
			allTimes := make([]time.Time, 0)
			for _, times := range requestTimes {
				allTimes = append(allTimes, times...)
			}
			sort.Slice(allTimes, func(i, j int) bool {
				return allTimes[i].Before(allTimes[j])
			})

			// Проверяем общее время
			totalTime := time.Since(start)
			totalReqs := tc.workers * tc.reqPerWorker
			limit := limiter.Limit()

			if limit > 0 {
				expectedMinTime := time.Duration(float64(totalReqs-tc.workers) / float64(limit) * float64(time.Second))
				if totalTime < expectedMinTime-100*time.Millisecond {
					t.Errorf("Total time %v is less than expected %v",
						totalTime, expectedMinTime)
				}
			}
		})
	}
}
