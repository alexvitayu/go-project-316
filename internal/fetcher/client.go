package fetcher

import (
	"code/internal/cache/assetscache"
	"code/internal/models"
	"code/internal/tools"
	"code/internal/tools/seen"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"golang.org/x/time/rate"
)

type FetchCrawlParams struct {
	QueueCh     chan models.AliveInnerLink
	Done        chan<- struct{}
	ErrsCh      chan<- error
	PagesCh     chan<- models.Page
	Index       int
	PendingURLs *int32
	Options     models.Options
	Visits      *seen.Visits
	Limiter     *rate.Limiter
	Cache       *assetscache.AssetsCache
	URLs        []string
	Item        models.AliveInnerLink
	BrLinks     *[]models.BrokenLink
	Client      http.Client
}

func ArrangeLinks(ctx context.Context, p FetchCrawlParams) error {
	for _, u := range p.URLs {
		if err := p.Limiter.Wait(ctx); err != nil {
			slog.Error("rate limiter", "error", err)
			atomic.AddInt32(p.PendingURLs, -1)
			return fmt.Errorf("rate limiter: %w", err)
		}

		resp, err := makeHEADorGETRequest(ctx, u, &p.Options)
		if err != nil {
			brokenLink := models.BrokenLink{
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
			*p.BrLinks = append(*p.BrLinks, models.BrokenLink{
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

func MakeGetRequest(ctx context.Context, url string, opts *models.Options) (*http.Response, error) {
	getReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	getReq.Header.Set("User-Agent", opts.UserAgent)

	getResp, err := DoRequestWithRetries(getReq, opts)
	if err != nil {
		return nil, err
	}
	return getResp, nil
}

func makeHEADorGETRequest(ctx context.Context, url string, opts *models.Options) (*http.Response, error) {
	headReq, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return MakeGetRequest(ctx, url, opts)
	}

	// Устанавливаем User-Agent (имитируем реальный браузер)
	headReq.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

	//headResp, err := opts.HTTPClient.Do(headReq)
	headResp, err := DoRequestWithRetries(headReq, opts)
	if err != nil {
		return MakeGetRequest(ctx, url, opts)
	}

	// При успешном HEAD запросе возвращаем response
	if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
		return headResp, nil
	}
	//Если статус-код не успешный, то закрывем тело head-запроса и делаем get-запрос
	if err := headResp.Body.Close(); err != nil {
		slog.Debug("failed to close HEAD response body", "error", err)
	}
	return MakeGetRequest(ctx, url, opts)
}
