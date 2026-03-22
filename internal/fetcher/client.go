package fetcher

import (
	"code/internal/models"
	"code/internal/tools"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

func makeHEADorGETRequest(ctx context.Context, url string, opts *models.Options, client models.HTTPClient) (*http.Response, error) {
	headReq, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return MakeGetRequest(ctx, url, opts, client)
	}

	// Устанавливаем User-Agent (имитируем реальный браузер)
	headReq.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

	//headResp, err := opts.HTTPClient.Do(headReq)
	headResp, err := DoRequestWithRetries(headReq, opts, client)
	if err != nil {
		return MakeGetRequest(ctx, url, opts, client)
	}

	// При успешном HEAD запросе возвращаем response
	if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
		return headResp, nil
	}
	//Если статус-код не успешный, то закрывем тело head-запроса и делаем get-запрос
	if err := headResp.Body.Close(); err != nil {
		slog.Debug("failed to close HEAD response body", "error", err)
	}
	return MakeGetRequest(ctx, url, opts, client)
}

func MakeGetRequest(ctx context.Context, url string, opts *models.Options, client models.HTTPClient) (*http.Response, error) {
	getReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	getReq.Header.Set("User-Agent", opts.UserAgent)

	getResp, err := DoRequestWithRetries(getReq, opts, client)
	if err != nil {
		return nil, err
	}
	return getResp, nil
}

func ArrangeLinks(p models.FetchCrawlParams) error {
	for _, u := range p.URLs {
		if err := p.Limiter.Wait(p.Ctx); err != nil {
			slog.Error("rate limiter", "error", err)
			atomic.AddInt32(p.PendingURLs, -1)
			return fmt.Errorf("rate limiter: %w", err)
		}

		resp, err := makeHEADorGETRequest(p.Ctx, u, &p.Options, p.Client)
		if err != nil {
			brokenLink := models.BrokenLinks{
				URL: u,
				Err: err.Error(),
			}

			if resp != nil {
				brokenLink.StatusCode = resp.StatusCode
			} else {
				if strings.Contains(err.Error(), "404") ||
					strings.Contains(err.Error(), "Not Found") {
					brokenLink.StatusCode = 404
				}

				if strings.Contains(u, "/missing") {
					brokenLink.StatusCode = 404
				}
			}

			*p.BrLinks = append(*p.BrLinks, brokenLink)
			continue
		}
		if err := resp.Body.Close(); err != nil {
			slog.Debug("failed to close response body", "error", err)
		}

		switch {
		case resp.StatusCode >= http.StatusBadRequest:
			*p.BrLinks = append(*p.BrLinks, models.BrokenLinks{
				URL:        u,
				StatusCode: resp.StatusCode,
				Err:        resp.Status,
			})

		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			normalizedURL := tools.NormalizeURL(u)
			if tools.IsInnerLink(normalizedURL, p.Item) && p.Item.LinkDepth < p.Options.Depth-1 {
				atomic.AddInt32(p.PendingURLs, 1)
				select {
				case p.QueueCh <- models.AliveInnerLink{
					URL:       normalizedURL,
					LinkDepth: p.Item.LinkDepth + 1,
				}:
				default:
					atomic.AddInt32(p.PendingURLs, -1)
					slog.Warn("queueCh is full")
				}
			}
		default:
			slog.Warn("unexpected status code", "url", u, "status_code", resp.StatusCode)
		}
	}
	return nil
}

func DoRequestWithRetries(req *http.Request, opts *models.Options, client models.HTTPClient) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= opts.Retries; attempt++ {
		resp, err = client.Do(req)

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
