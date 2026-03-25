package fetcher

import (
	"code/internal/models"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func DoRequestWithRetries(req *http.Request, opts *models.Options) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= opts.Retries; attempt++ {
		resp, err = opts.HTTPClient.Do(req)

		shouldRetry := false

		if err != nil {
			if isNetworkError(err) {
				shouldRetry = true
				slog.Debug("network error, retrying request",
					"attempt", attempt,
					"error", err,
					"url", req.URL.String())
			}
		} else {
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				shouldRetry = true
				slog.Debug("retrying request",
					"attempt", attempt,
					"statusCode", resp.StatusCode,
					"url", req.URL.String())
			}
		}

		if !shouldRetry {
			return resp, err
		}

		if attempt == opts.Retries {
			return resp, err
		}

		if resp != nil {
			if respErr := resp.Body.Close(); respErr != nil {
				slog.Debug("failed to close response body during retry",
					"attempt", attempt,
					"error", respErr)
			}
		}

		time.Sleep(100 * time.Millisecond)
	}
	return resp, err
}

func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	if strings.Contains(err.Error(), "connection reset") ||
		strings.Contains(err.Error(), "broken pipe") ||
		strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "network is unreachable") {
		return true
	}
	return false
}
