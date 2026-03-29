package fetcher

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRequestWithRetries_NoError(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name             string
		method           string
		retries          int
		handler          func(*int) http.HandlerFunc
		wantRequestCount int
		wantStatus       int
	}{
		{
			name:    "429_response",
			method:  "HEAD",
			retries: 3,
			handler: func(counter *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					(*counter)++ // увеличиваем счетчик

					if *counter <= 2 {
						w.WriteHeader(http.StatusTooManyRequests)
						if _, err := w.Write([]byte("too many requests")); err != nil {
							_ = err
						}
					} else {
						w.WriteHeader(http.StatusOK)
						if _, err := w.Write([]byte("OK")); err != nil {
							_ = err
						}
					}
				}
			},
			wantRequestCount: 3,
			wantStatus:       http.StatusOK,
		},
		{
			name:    "500_response",
			method:  "GET",
			retries: 4,
			handler: func(counter *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					(*counter)++ // увеличиваем счетчик

					if *counter <= 3 {
						w.WriteHeader(http.StatusInternalServerError)
						if _, err := w.Write([]byte("internal server error")); err != nil {
							_ = err
						}
					} else {
						w.WriteHeader(http.StatusOK)
						if _, err := w.Write([]byte("OK")); err != nil {
							_ = err
						}
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
			client := &http.Client{}

			handler := tc.handler(&requestCount)

			server := httptest.NewServer(handler)
			defer server.Close()

			req, err := http.NewRequestWithContext(t.Context(), tc.method, server.URL, nil)
			require.NoError(t, err)

			resp, err := DoRequestWithRetries(req, tc.retries, client)
			require.NoError(t, err)
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Logf("failed to close response body: %v", err)
				}
			}()

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
		retries          int
		responses        []response
		wantRequestCount int
		isErr            bool
		wantStatus       int
		wantErr          string
	}{
		{
			name:    "429_response -> retry -> success",
			method:  "HEAD",
			retries: 1,
			responses: []response{
				{429, nil},
				{200, nil},
			},
			wantRequestCount: 2,
			wantStatus:       http.StatusOK,
		},
		{
			name:    "500_response -> two_retries -> success",
			method:  "GET",
			retries: 2,
			responses: []response{
				{500, nil},
				{500, nil},
				{200, nil},
			},
			wantRequestCount: 3,
			wantStatus:       http.StatusOK,
		},
		{
			name:    "one_retry_with_error -> error",
			method:  "GET",
			retries: 1,
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
			name:    "second_retry_successful -> success",
			method:  "GET",
			retries: 2,
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
			client := &http.Client{}
			client.Transport = transport

			req, err := http.NewRequestWithContext(t.Context(), tc.method, "https://test.com", nil)
			require.NoError(t, err)

			resp, err := DoRequestWithRetries(req, tc.retries, client)
			if tc.isErr {
				require.Error(t, err)
				if tc.wantErr != "" {
					assert.Contains(t, err.Error(), tc.wantErr)
				}
			} else {
				require.NoError(t, err)
				defer func() {
					if err := resp.Body.Close(); err != nil {
						t.Logf("failed to close response body: %v", err)
					}
				}()
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
