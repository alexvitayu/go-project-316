package crawler_test

import (
	"bytes"
	"code/internal/code/crawler"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestAnalyze(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name         string
		handler      http.HandlerFunc
		options      func(server *httptest.Server) crawler.Options
		wantResponse crawler.Response
		wantErr      error
		thereIsErr   bool
	}{
		{
			name: "200_response",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				response := crawler.Response{
					RootURL: r.Host, //или server.URL
					Pages: []crawler.Page{
						{
							URL:        r.Host,
							HTTPStatus: http.StatusOK,
							Status:     "200 OK",
							Error:      "",
						},
					},
				}
				err := json.NewEncoder(w).Encode(response)
				assert.NoError(t, err)
			}),
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:         server.URL,
					HTTPClient:  &http.Client{},
					Timeout:     30 * time.Second,
					Concurrency: 2,
				}
			},
			wantResponse: crawler.Response{
				Pages: []crawler.Page{
					{
						HTTPStatus: http.StatusOK,
						Status:     "200 OK",
						Error:      "",
					},
				},
			},
			wantErr:    nil,
			thereIsErr: false,
		},
		{
			name: "404_Not_Found",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Header().Set("Content-Type", "application/json")
				response := crawler.Response{
					RootURL: r.Host, //или server.URL
					Pages: []crawler.Page{
						{
							URL:        r.Host,
							HTTPStatus: http.StatusNotFound,
							Status:     "404 Not Found",
							Error:      "",
						},
					},
				}
				err := json.NewEncoder(w).Encode(response)
				assert.NoError(t, err)
			}),
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:         server.URL,
					HTTPClient:  &http.Client{},
					Timeout:     15 * time.Second,
					Concurrency: 1,
				}
			},
			wantResponse: crawler.Response{
				Pages: []crawler.Page{
					{
						HTTPStatus: http.StatusNotFound,
						Status:     "404 Not Found",
						Error:      "",
					},
				},
			},
			wantErr:    nil,
			thereIsErr: false,
		},
		{
			name: "700_invalid_status",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(700)
				w.Header().Set("Content-Type", "application/json")
				response := crawler.Response{
					RootURL: r.Host, //или server.URL
					Pages: []crawler.Page{
						{
							URL:        r.Host,
							HTTPStatus: 700,
							Error:      "",
						},
					},
				}
				err := json.NewEncoder(w).Encode(response)
				assert.NoError(t, err)
			}),
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:         server.URL,
					HTTPClient:  &http.Client{},
					Timeout:     15 * time.Second,
					Concurrency: 4,
				}
			},
			wantResponse: crawler.Response{
				Pages: []crawler.Page{
					{
						HTTPStatus: 700,
						Error:      "",
					},
				},
			},
			wantErr:    nil,
			thereIsErr: false,
		},
		{
			name: "timeout_exceeded",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(5 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			},
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL: server.URL,
					HTTPClient: &http.Client{
						Timeout: 2 * time.Millisecond,
					},
					Timeout:     3 * time.Second,
					Concurrency: 1,
				}
			},
			wantErr:    context.DeadlineExceeded,
			thereIsErr: true,
		},
		{
			name: "connection_refused",
			options: func(_ *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:         "http://localhost:65535",
					HTTPClient:  &http.Client{},
					Timeout:     1 * time.Second,
					Concurrency: 1,
				}
			},
			wantErr:    syscall.ECONNREFUSED,
			thereIsErr: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			got, err := crawler.Analyze(t.Context(), tc.options(server))
			if tc.thereIsErr {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)

			var response crawler.Response
			jErr := json.Unmarshal(got, &response)
			require.NoError(t, jErr)

			assert.Equal(t, tc.wantResponse.Pages[0].HTTPStatus, response.Pages[0].HTTPStatus)
			assert.Equal(t, tc.wantResponse.Pages[0].Error, response.Pages[0].Error)
		})
	}
}

func TestArrangeLinks(t *testing.T) {
	t.Parallel()

	t.Run("find_broken_links", func(t *testing.T) {
		t.Parallel()
		opts := crawler.Options{
			HTTPClient: &http.Client{},
			UserAgent:  "curl/8.14.1",
		}
		sourceLinks := []string{"https://www.google.com", "https://github.com",
			"https://www.google.com/non-existent-page", "https://wooordhunt.ru/word/хтрй"}

		item := crawler.AliveInnerLink{}
		queueCh := make(chan crawler.AliveInnerLink)
		var pendingURLs int32 = 0
		limiter := rate.NewLimiter(rate.Inf, 1)

		wantLengthBroken := 2

		broken, err := crawler.ArrangeLinks(t.Context(), sourceLinks, opts, item, queueCh, &pendingURLs, limiter)
		require.NoError(t, err)
		assert.Len(t, broken, wantLengthBroken)
	})
}

func TestCollectSEO(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name    string
		handler http.Handler
		wantSEO *crawler.Seo
	}{
		{
			name: "seo_positive_test",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.ServeFile(writer, request, "/home/alex/go-project-316/testdata/seo_test/positive_test.html")
			}),
			wantSEO: &crawler.Seo{
				HasTitle:       true,
				Title:          "My first site",
				HasDescription: true,
				Description:    "Programing & learning",
				HasH1:          true,
			},
		},
		{
			name: "seo_negative_test",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.ServeFile(writer, request, "/home/alex/go-project-316/testdata/seo_test/negative_test.html")
			}),
			wantSEO: &crawler.Seo{
				HasTitle:       false,
				Title:          "",
				HasDescription: false,
				Description:    "",
				HasH1:          false,
			},
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(tc.handler)
			client := http.Client{}
			resp, err := client.Get(server.URL)
			require.NoError(t, err)
			seo, err := crawler.CollectSEO(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.wantSEO, seo)
			resp.Body.Close()
		})
	}
}

func TestSetLimit(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name      string
		opts      crawler.Options
		wantLimit float64
	}{
		{
			name:      "not_set_rps_and_delay",
			opts:      crawler.Options{},
			wantLimit: 0,
		},
		{
			name: "set_delay_millisecond",
			opts: crawler.Options{
				Delay: 250 * time.Millisecond,
			},
			wantLimit: 4,
		},
		{
			name: "set_delay_second",
			opts: crawler.Options{
				Delay: 4 * time.Second,
			},
			wantLimit: 0.25,
		},
		{
			name: "set_rps_and_delay",
			opts: crawler.Options{
				Delay: 4 * time.Second,
				RPS:   3,
			},
			wantLimit: 3,
		},
		{
			name: "set_rps",
			opts: crawler.Options{
				RPS: 4,
			},
			wantLimit: 4,
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := crawler.SetLimit(&tc.opts)
			assert.InDelta(t, tc.wantLimit, got, 0.0001)
		})
	}
}

func TestLimiter_TimeBetweenRequests(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		rps          int
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
			limiter := rate.NewLimiter(rate.Limit(crawler.SetLimit(&crawler.Options{
				Delay: tc.delay,
				RPS:   tc.rps,
			})), tc.workers)

			var wg sync.WaitGroup
			requestTimes := make([][]time.Time, tc.workers) // вместит в себя сумму всех времён запросов от всех воркеров

			start := time.Now()

			for w := 0; w < tc.workers; w++ {
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

func TestDoRequestWithRetries_NoError(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name             string
		method           string
		opts             crawler.Options
		handler          func(*int) http.HandlerFunc
		wantRequestCount int
		wantStatus       int
	}{
		{
			name:   "429_response",
			method: "HEAD",
			opts: crawler.Options{
				Retries:    3,
				HTTPClient: &http.Client{},
			},
			handler: func(counter *int) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					(*counter)++ // увеличиваем счетчик

					if *counter <= 2 {
						w.WriteHeader(http.StatusTooManyRequests)
						w.Write([]byte("too many requests"))
					} else {
						w.WriteHeader(http.StatusOK)
						w.Write([]byte("OK"))
					}
				}
			},
			wantRequestCount: 3,
			wantStatus:       http.StatusOK,
		},
		{
			name:   "500_response",
			method: "GET",
			opts: crawler.Options{
				Retries:    4,
				HTTPClient: &http.Client{},
			},
			handler: func(counter *int) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					(*counter)++ // увеличиваем счетчик

					if *counter <= 3 {
						w.WriteHeader(http.StatusInternalServerError)
						w.Write([]byte("internal server error"))
					} else {
						w.WriteHeader(http.StatusOK)
						w.Write([]byte("OK"))
					}
				}
			},
			wantRequestCount: 4,
			wantStatus:       http.StatusOK,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requestCount := 0

			handler := tc.handler(&requestCount)

			server := httptest.NewServer(handler)
			defer server.Close()

			req, err := http.NewRequestWithContext(t.Context(), tc.method, server.URL, nil)
			require.NoError(t, err)

			resp, err := crawler.DoRequestWithRetries(req, tc.opts)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			assert.Equal(t, tc.wantRequestCount, requestCount)
		})
	}
}

// В данном тесте заменим у http клиента с помощью метода Transport, DefaultTransport который имплементирует
// интерфейс RoundTripper, на кастомный transport, который будет возвращать заранее подготовленные данные.
func TestDoRequestWithRetries_WithError(t *testing.T) {
	t.Parallel()

	type response struct {
		statusCode int
		err        error
	}
	var testCases = []struct {
		name             string
		method           string
		opts             crawler.Options
		responses        []response
		wantRequestCount int
		isErr            bool
		wantStatus       int
		wantErr          string
	}{
		{
			name:   "429_response -> retry -> success",
			method: "HEAD",
			opts: crawler.Options{
				Retries:    1,
				HTTPClient: &http.Client{},
			},
			responses: []response{
				{429, nil},
				{200, nil},
			},
			wantRequestCount: 2,
			wantStatus:       http.StatusOK,
		},
		{
			name:   "500_response -> two_retries -> success",
			method: "GET",
			opts: crawler.Options{
				Retries:    2,
				HTTPClient: &http.Client{},
			},
			responses: []response{
				{500, nil},
				{500, nil},
				{200, nil},
			},
			wantRequestCount: 3,
			wantStatus:       http.StatusOK,
		},
		{
			name:   "one_retry_with_error -> error",
			method: "GET",
			opts: crawler.Options{
				Retries:    1,
				HTTPClient: &http.Client{},
			},
			responses: []response{
				{0, &net.OpError{
					Err: &os.SyscallError{
						Syscall: "connect",
						Err:     syscall.ECONNREFUSED,
					},
				}},
				{0, &net.OpError{
					Err: &os.SyscallError{
						Syscall: "connect",
						Err:     syscall.ENETUNREACH,
					},
				}},
			},
			wantRequestCount: 2,
			wantStatus:       0,
			wantErr:          "network is unreachable",
			isErr:            true,
		},
		{
			name:   "second_retry_successful -> success",
			method: "GET",
			opts: crawler.Options{
				Retries:    2,
				HTTPClient: &http.Client{},
			},
			responses: []response{
				{0, &net.OpError{
					Err: &os.SyscallError{
						Syscall: "connect",
						Err:     syscall.ENETUNREACH,
					},
				}},
				{0, &net.OpError{
					Err: &os.SyscallError{
						Syscall: "connect",
						Err:     syscall.ENETUNREACH,
					},
				}},
				{200, nil},
			},
			wantRequestCount: 3,
			wantStatus:       200,
			isErr:            false,
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			requestCount := 0
			transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if requestCount >= len(tc.responses) {
					return nil, fmt.Errorf("unexpected request #%d", requestCount)
				}

				respSeq := tc.responses[requestCount]
				requestCount++

				if respSeq.err != nil {
					return nil, respSeq.err
				}
				return &http.Response{
					StatusCode: respSeq.statusCode,
					Status:     http.StatusText(respSeq.statusCode),
					Body:       io.NopCloser(bytes.NewBufferString("response")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			})
			tc.opts.HTTPClient.Transport = transport

			req, err := http.NewRequestWithContext(t.Context(), tc.method, "https://test.com", nil)
			require.NoError(t, err)

			resp, err := crawler.DoRequestWithRetries(req, tc.opts)
			if tc.isErr {
				require.Error(t, err)
				if tc.wantErr != "" {
					assert.Contains(t, err.Error(), tc.wantErr)
				}
			} else {
				require.NoError(t, err)
				defer resp.Body.Close()
				assert.Equal(t, tc.wantStatus, resp.StatusCode)
			}

			assert.Equal(t, tc.wantRequestCount, requestCount)
		})
	}
}

type roundTripperFunc func(r *http.Request) (*http.Response, error)

// создадим метод, чтобы реализовать type RoundTripper interface
func (rf roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return rf(req)
}
