package fetcher

import (
	"code/internal/cache/assetscache"
	"code/internal/cache/linkscache"
	"code/internal/models"
	"code/internal/tools"
	"code/internal/tools/seen"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

type FetchCrawlParams struct {
	BaseURL     string
	QueueCh     chan models.AliveInnerLink
	WorkerWG    *sync.WaitGroup
	TasksWG     *sync.WaitGroup
	ErrsCh      chan<- error
	PagesCh     chan<- models.Page
	Index       int
	Retries     int
	UserAgent   string
	Depth       int
	Visits      *seen.Visits
	Limiter     *rate.Limiter
	AssetsCache *assetscache.AssetsCache
	LinksCache  *linkscache.LinksCache
	URLs        []string
	Item        models.AliveInnerLink
	BrLinks     *[]models.BrokenLink
	Client      *http.Client
}

func ArrangeLinks(ctx context.Context, p FetchCrawlParams) error {
	for _, u := range p.URLs {
		if err := p.Limiter.Wait(ctx); err != nil {
			slog.Error("rate limiter", "error", err)
			p.TasksWG.Done()
			return fmt.Errorf("rate limiter: %w", err)
		}

		var resp *http.Response
		var err error
		var brokenLink models.BrokenLink

		if p.LinksCache.IsThereInCache(u) {
			resp, err = p.LinksCache.TakeFromCache(u)
		} else {
			resp, err = makeHEADorGETRequest(ctx, u, p.Retries, p.UserAgent, p.Client)
			p.LinksCache.AddToCache(u, err, resp)
		}
		if err != nil {
			brokenLink = models.BrokenLink{
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
			if tools.IsInnerLink(normalizedURL, p.Item) && p.Item.LinkDepth < p.Depth-1 {
				p.TasksWG.Add(1)
				select {
				case p.QueueCh <- models.AliveInnerLink{
					URL:       normalizedURL,
					LinkDepth: p.Item.LinkDepth + 1,
				}:
				default:
					p.TasksWG.Done()
					slog.Warn("queueCh is full")
				}
			}
		default:
			slog.Warn("unexpected status code", "url", u, "status_code", resp.StatusCode)
		}
	}
	return nil
}

func MakeGetRequest(ctx context.Context, url string, userAgent string, retries int, c *http.Client) (*http.Response, error) {
	getReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	getReq.Header.Set("User-Agent", userAgent)

	getResp, err := DoRequestWithRetries(getReq, retries, c)
	if err != nil {
		return nil, err
	}
	return getResp, nil
}

func makeHEADorGETRequest(ctx context.Context, url string, retries int, userAgent string, c *http.Client) (*http.Response, error) {
	headReq, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return MakeGetRequest(ctx, url, userAgent, retries, c)
	}

	// Устанавливаем User-Agent (имитируем реальный браузер)
	headReq.Header.Set("User-Agent", userAgent) //обход блокировок на некоторых сайтах
	headResp, err := DoRequestWithRetries(headReq, retries, c)
	if err != nil {
		return MakeGetRequest(ctx, url, userAgent, retries, c)
	}

	// При успешном HEAD запросе возвращаем response
	if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
		return headResp, nil
	}
	//Если статус-код не успешный, то закрывем тело head-запроса и делаем get-запрос
	if err := headResp.Body.Close(); err != nil {
		slog.Debug("failed to close HEAD response body", "error", err)
	}
	return MakeGetRequest(ctx, url, userAgent, retries, c)
}
