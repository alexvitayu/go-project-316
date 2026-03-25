package crawler

import (
	"bytes"
	"code/internal/cache/assetscache"
	"code/internal/fetcher"
	"code/internal/limiter"
	"code/internal/models"
	"code/internal/parser"
	"code/internal/tools"
	"code/internal/tools/seen"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/time/rate"
)

const initQueueCapacity = 500

// Analyze - Основная точка входа в crawler
func Analyze(c context.Context, opts models.Options) ([]byte, error) {
	// Обработка флага timeout
	ctx, cancel := context.WithTimeout(c, opts.Timeout)
	defer cancel()

	// Обработка Ctrl + C
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		cancel() // отменяем контекст при получении сигнала
	}()

	// введём limiter, с его помощью ограничиваем число запросов в единицу времени и используем его
	// как шлагбаум в местах запросов
	limiter := rate.NewLimiter(limiter.SetUpLimit(limiter.FindOutRPS(&opts)), opts.Concurrency)

	// инициализируем структуру, которая собирает посещения и контроллирует конкурентный доступ к данным
	visits := seen.NewVisits()

	// инициализируем каналы для очереди адресов, ошибок, обработанных страниц и сигнальный канал done вместо wg
	queueCh := make(chan models.AliveInnerLink, initQueueCapacity)
	errsCh := make(chan error, opts.Concurrency*2)
	pagesCh := make(chan models.Page, 100)
	done := make(chan struct{}, opts.Concurrency)

	// Счётчик активных задач в очереди
	var pendingURLs int32

	// добавляем в очередь базовый url с глубиной 0
	atomic.AddInt32(&pendingURLs, 1)
	queueCh <- models.AliveInnerLink{
		URL:       opts.URL,
		LinkDepth: 0,
	}

	// инициализируем AssetsCache, чтобы передать его каждому воркеру
	cache := assetscache.NewCacheAssets()

	params := &fetcher.FetchCrawlParams{
		QueueCh:     queueCh,
		Done:        done,
		ErrsCh:      errsCh,
		PagesCh:     pagesCh,
		PendingURLs: &pendingURLs,
		Options:     opts,
		Visits:      visits,
		Limiter:     limiter,
		Cache:       cache,
	}

	for i := 1; i <= opts.Concurrency; i++ {
		params.Index = i
		localParams := *params

		go func() {
			crawlWorker(ctx, localParams)
		}()
	}

	// это отдельная горутина-наблюдатель, которая ждёт завершения всех задач либо отмены контекста
	go func() {
		for {
			select {
			case <-ctx.Done():
				slog.Debug("context done, stopping crawler", "ctx_error", ctx.Err())
				// Принудительно закрываем каналы
				close(queueCh)
				// Ждём завершения воркеров
				for range opts.Concurrency { // каждый из воркеров должен прислать сигнал done
					<-done
				}
				close(errsCh)
				close(pagesCh)
				slog.Debug("all channels have been closed")
				return

			case <-time.After(100 * time.Millisecond): // опрос через каждые 100 миллисекунд, чтобы не грузить процессор
				if atomic.LoadInt32(&pendingURLs) == 0 {
					slog.Debug("all URLs processed, shutting down")
					close(queueCh)
					// Ждём завершения воркеров
					for range opts.Concurrency {
						<-done
					}
					close(errsCh)
					close(pagesCh)
					slog.Debug("all channels have been closed")
					return
				}
			}
		}
	}()

	// блок сбора результатов
	var pages []models.Page
	var errs []error

	for pagesCh != nil || errsCh != nil {
		select {
		case p, ok := <-pagesCh:
			if !ok {
				pagesCh = nil
				continue
			}
			pages = append(pages, p)

		case err, ok := <-errsCh:
			if !ok {
				errsCh = nil
				continue
			}
			errs = append(errs, err)
		}
	}

	// отсортируем страницы по URL
	normalizePages(pages)

	// возвращаем первую ошибку
	var firstErr error
	if len(errs) > 0 {
		firstErr = errs[0]
		if len(pages) == 0 {
			pages = append(pages, models.Page{
				URL:    tools.NormalizeURL(opts.URL),
				Status: "error",
				Error:  firstErr.Error(),
				SEO:    &models.SEO{},
			})
		}
	}

	data := models.Report{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Pages:       pages,
	}

	return ReturnReport(&data, opts.IndentJSON, firstErr)
}

func crawlWorker(ctx context.Context, p fetcher.FetchCrawlParams) {
	slog.Debug("goroutine started", "goroutine_id", p.Index, "status", "running")

	defer func() {
		p.Done <- struct{}{} //сигнал от каждой горутины, что работа окончена
		slog.Debug("goroutine finished", "goroutine_id", p.Index, "status", "finished")
	}()

	for item := range p.QueueCh {
		var page models.Page
		p.Item = item

		if ctx.Err() != nil { // истёк таймаут или произошла отмена Ctrl + C
			slog.Debug("context done, stopping worker", "goroutine_id", p.Index)
			page.Error = ctx.Err().Error()
			p.ErrsCh <- fmt.Errorf("context: %w", ctx.Err())
			atomic.AddInt32(p.PendingURLs, -1)
			continue
		}

		if item.LinkDepth > p.Options.Depth {
			atomic.AddInt32(p.PendingURLs, -1)
			continue
		}

		p.Visits.Mu.Lock()

		normalizedURL := tools.NormalizeURL(item.URL)

		if _, ok := p.Visits.IsVisited[normalizedURL]; ok {
			p.Visits.Mu.Unlock()
			atomic.AddInt32(p.PendingURLs, -1)
			continue
		}

		p.Visits.IsVisited[normalizedURL] = struct{}{}
		p.Visits.Mu.Unlock()

		// теперь обрабатываем страницу
		// Создаем новый запрос
		if err := p.Limiter.Wait(ctx); err != nil {
			slog.Warn("rate limiter, method wait failed", "error", err)
			p.ErrsCh <- fmt.Errorf("rate limiter: %w", err)
			atomic.AddInt32(p.PendingURLs, -1)
			return
		}

		req, err := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
		if err != nil {
			page.Error = err.Error()
			p.ErrsCh <- fmt.Errorf("failed to make request: %w", err)
			continue
		}

		// Устанавливаем User-Agent (имитируем реальный браузер)
		req.Header.Set("User-Agent", p.Options.UserAgent) //обход блокировок на некоторых сайтах

		brLinks := make([]models.BrokenLink, 0)
		p.BrLinks = &brLinks

		// Выполняем запрос
		resp, err := fetcher.DoRequestWithRetries(req, &p.Options)
		if err != nil {
			brLinks = append(brLinks, models.BrokenLink{
				URL: item.URL,
				Err: err.Error(),
			})
			if resp != nil {
				if err = resp.Body.Close(); err != nil {
					slog.Debug("failed to close response body", "error", err)
				}
			}
			p.ErrsCh <- err
			continue
		}

		if resp.StatusCode >= http.StatusBadRequest {
			brLinks = append(brLinks, models.BrokenLink{
				URL:        item.URL,
				StatusCode: resp.StatusCode,
				//Err:        resp.Status,
			})
		}

		// Сохраним body для дальнейшей работы в разных местах
		savedBody, err := io.ReadAll(resp.Body)
		if respErr := resp.Body.Close(); err != nil {
			slog.Debug("failed to close response body", "error", respErr)
		}
		if err != nil {
			page.Error = err.Error()
			p.ErrsCh <- fmt.Errorf("failed to save body: %w", err)
			continue
		}

		// Извлекаем ссылки из страницы
		links := parser.ParseHTML(bytes.NewReader(savedBody))

		// Преобразуем ссылки в абсолютные url и убираем дублирующиеся
		URLs, err := tools.ProcessLinks(links, &p.Options)
		if err != nil {
			page.Error = err.Error()
			p.ErrsCh <- fmt.Errorf("ProcessLinks: %w", err)
			continue
		}
		p.URLs = URLs

		err = fetcher.ArrangeLinks(ctx, p)
		if err != nil {
			page.Error = err.Error()
			p.ErrsCh <- fmt.Errorf("ArrangeLinks: %w", err)
			continue
		}

		// Соберём SEO из полученной html страницы
		seo, err := parser.CollectSEO(bytes.NewReader(savedBody))
		if err != nil {
			page.Error = err.Error()
			p.ErrsCh <- fmt.Errorf("CollectSEO: %w", err)
			continue
		}

		assetPrms := parser.FetchCollectParams{
			Opts:    p.Options,
			BaseURL: item.URL,
			Body:    bytes.NewReader(savedBody),
			Cache:   p.Cache,
		}

		assets, err := parser.CollectAssets(ctx, assetPrms)
		if err != nil {
			page.Error = err.Error()
			p.ErrsCh <- fmt.Errorf("CollectAssets: %w", err)
			continue
		}

		var status string
		if resp.Status == "200 OK" {
			status = "ok"
		} else {
			status = resp.Status
		}

		page.URL = tools.NormalizeURL(item.URL)
		page.Depth = item.LinkDepth
		page.HTTPStatus = resp.StatusCode
		page.Status = status
		page.SEO = seo
		page.BrokenLinks = brLinks
		page.Assets = assets
		page.DiscoveredAt = time.Now().Format(time.RFC3339)

		p.PagesCh <- page

		atomic.AddInt32(p.PendingURLs, -1) // уменьшаем счётчик urls в ожидании обработки
	}
}

func ReturnReport(data *models.Report, indent bool, firstError error) ([]byte, error) {
	var (
		report []byte
		err    error
	)

	if indent {
		report, err = json.MarshalIndent(data, "", " ")
	} else {
		report, err = json.Marshal(data)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to marshal report: %w", err)
	}
	report = append(report, '\n') // завершающий перевод строки

	return report, firstError
}

func normalizePages(pages []models.Page) {
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].URL < pages[j].URL
	})
}
