package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/time/rate"
)

const initQueueCapacity = 500

var supportedSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

type Options struct {
	URL         string
	Depth       int
	Retries     int
	Delay       time.Duration
	Timeout     time.Duration
	UserAgent   string
	Concurrency int
	IndentJSON  bool
	HTTPClient  *http.Client
	RPS         int
}

type Page struct {
	URL          string        `json:"url"`
	Depth        int           `json:"depth"`
	HTTPStatus   int           `json:"http_status"`
	Status       string        `json:"status"`
	Error        string        `json:"error,omitempty"`
	SEO          *SEO          `json:"seo"`
	BrokenLinks  []BrokenLinks `json:"broken_links"`
	Assets       []Assets      `json:"assets"`
	DiscoveredAt string        `json:"discovered_at"`
}

type BrokenLinks struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Err        string `json:"error"`
}

type SEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

type Assets struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	StatusCode int    `json:"status_code"`
	SizeBytes  int64  `json:"size_bytes"`
	Error      string `json:"error,omitempty"`
}

type Report struct {
	RootURL     string `json:"root_url"`
	Depth       int    `json:"depth"`
	GeneratedAt string `json:"generated_at"`
	Pages       []Page `json:"pages"`
}

type AliveInnerLink struct {
	URL       string
	LinkDepth int
}

// AssetsCache здесь реализуем кэш ассетов
type AssetsCache struct {
	cache map[string]Assets
	mu    *sync.Mutex
}

func NewCacheAssets() *AssetsCache {
	return &AssetsCache{
		cache: make(map[string]Assets),
		mu:    &sync.Mutex{},
	}
}

func (c *AssetsCache) AddToCache(url string, assets Assets) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.cache[url]; !exists {
		c.cache[url] = assets
	}
}

func (c *AssetsCache) IsThereInCache(url string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.cache[url]
	return ok
}

func (c *AssetsCache) TakeFromCache(url string) Assets {
	c.mu.Lock()
	defer c.mu.Unlock()
	asset, exists := c.cache[url]
	if exists {
		return asset
	}
	return Assets{}
}

type Visits struct {
	mu        *sync.Mutex
	isVisited map[string]struct{}
}

func NewVisits() *Visits {
	return &Visits{
		isVisited: make(map[string]struct{}),
		mu:        &sync.Mutex{},
	}
}

// Analyze - Основная точка входа в crawler
func Analyze(c context.Context, opts Options) ([]byte, error) {
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
	rps := SetLimit(&opts)
	var limit rate.Limit
	if rps <= 0 {
		limit = rate.Inf // количество запросов за секунду не ограничено
	} else if rps > 0 {
		limit = rate.Limit(rps)
	}
	limiter := rate.NewLimiter(limit, opts.Concurrency)

	// инициализируем структуру, которая собирает посещения и контроллирует конкурентный доступ к данным
	visits := NewVisits()

	// инициализируем каналы для очереди адресов, ошибок, обработанных страниц и сигнальный канал done вместо wg
	queueCh := make(chan AliveInnerLink, initQueueCapacity)
	errsCh := make(chan error, opts.Concurrency*2)
	pagesCh := make(chan Page, 100)
	done := make(chan struct{}, opts.Concurrency)

	// Счётчик активных задач в очереди
	var pendingURLs int32

	// добавляем в очередь базовый url с глубиной 0
	atomic.AddInt32(&pendingURLs, 1)
	queueCh <- AliveInnerLink{
		URL:       opts.URL,
		LinkDepth: 0,
	}

	// инициализируем AssetsCache, чтобы передать его каждому воркеру
	cache := NewCacheAssets()

	for i := 1; i <= opts.Concurrency; i++ {
		go func(indx int) {
			crawlWorker(ctx, queueCh, done, errsCh, pagesCh, indx, &pendingURLs, opts, visits, limiter, cache)
		}(i)
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
	var pages []Page
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

	// возвращаем первую ошибку
	var firstErr error
	if len(errs) > 0 {
		firstErr = errs[0]
	}

	data := Report{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Pages:       pages,
	}

	return ReturnReport(&data, opts.IndentJSON, firstErr)
}

func ReturnReport(data *Report, indent bool, firstError error) ([]byte, error) {
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

func crawlWorker(ctx context.Context, queueCh chan AliveInnerLink, done chan<- struct{}, errsCh chan<- error,
	pagesCh chan<- Page, indx int, pendingURLs *int32, opts Options, visits *Visits, limiter *rate.Limiter, cache *AssetsCache) {
	slog.Debug("goroutine started", "goroutine_id", indx, "status", "running")

	defer func() {
		done <- struct{}{} //сигнал от каждой горутины, что работа окончена
		slog.Debug("goroutine finished", "goroutine_id", indx, "status", "finished")
	}()

	for item := range queueCh {
		var page Page

		if ctx.Err() != nil { // истёк таймаут или произошла отмена Ctrl + C
			slog.Debug("context done, stopping worker", "goroutine_id", indx)
			page.Error = ctx.Err().Error()
			errsCh <- fmt.Errorf("context: %w", ctx.Err())
			atomic.AddInt32(pendingURLs, -1)
			continue
		}

		if item.LinkDepth > opts.Depth {
			atomic.AddInt32(pendingURLs, -1)
			continue
		}

		visits.mu.Lock()
		if _, ok := visits.isVisited[item.URL]; ok {
			visits.mu.Unlock()
			atomic.AddInt32(pendingURLs, -1)
			continue
		}
		visits.isVisited[item.URL] = struct{}{}
		visits.mu.Unlock()

		// теперь обрабатываем страницу
		// Создаем новый запрос
		if err := limiter.Wait(ctx); err != nil {
			slog.Warn("rate limiter, method wait failed", "error", err)
			errsCh <- fmt.Errorf("rate limiter: %w", err)
			atomic.AddInt32(pendingURLs, -1)
			return
		}
		req, err := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
		if err != nil {
			page.Error = err.Error()
			errsCh <- fmt.Errorf("failed to make request: %w", err)
			continue
		}

		// Устанавливаем User-Agent (имитируем реальный браузер)
		req.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

		// Выполняем запрос
		resp, err := DoRequestWithRetries(req, opts)
		if err != nil {
			if resp != nil {
				resp.Body.Close() // Даже при ошибке resp может быть не nil!
			}
			errsCh <- fmt.Errorf("get request failed: %w", err)
			continue
		}

		// Сохраним body для дальнейшей работы в разных местах
		savedBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			page.Error = err.Error()
			errsCh <- fmt.Errorf("failed to save body: %w", err)
			continue
		}

		// Извлекаем ссылки из страницы
		links := checkHTML(bytes.NewReader(savedBody))

		// Преобразуем ссылки в абсолютные url и убираем дублирующиеся
		URLs, err := ProcessLinks(links, &opts)
		if err != nil {
			page.Error = err.Error()
			errsCh <- fmt.Errorf("ProcessLinks: %w", err)
			continue
		}

		brLinks, err := ArrangeLinks(ctx, URLs, opts, item, queueCh, pendingURLs, limiter)
		if err != nil {
			page.Error = err.Error()
			errsCh <- fmt.Errorf("ArrangeLinks: %w", err)
			continue
		}

		// Соберём SEO из полученной html страницы
		seo, err := CollectSEO(bytes.NewReader(savedBody))
		if err != nil {
			page.Error = err.Error()
			errsCh <- fmt.Errorf("CollectSEO: %w", err)
			continue
		}

		assets, err := CollectAssets(ctx, opts, item.URL, bytes.NewReader(savedBody), cache)
		if err != nil {
			page.Error = err.Error()
			errsCh <- fmt.Errorf("CollectAssets: %w", err)
			continue
		}

		var status string
		if resp.Status == "200 OK" {
			status = "ok"
		} else {
			status = resp.Status
		}

		page.URL = item.URL
		page.Depth = item.LinkDepth
		page.HTTPStatus = resp.StatusCode
		page.Status = status
		page.SEO = seo
		page.BrokenLinks = brLinks
		page.Assets = assets
		page.DiscoveredAt = time.Now().Format(time.RFC3339)

		pagesCh <- page

		atomic.AddInt32(pendingURLs, -1) // уменьшаем счётчик urls в ожидании обработки
	}
}

func checkHTML(r io.Reader) []string {
	var links []string

	doc, err := html.Parse(r)
	if err != nil {
		slog.Error("checkHTML", "parse html failed", err)
		return nil
	}

	for n := range doc.Descendants() { // итерируемся по потомкам descendants
		if n.Type == html.ElementNode && n.DataAtom == atom.A { // atom.A это то же самое, что и тег <a> (ссылка)
			for _, a := range n.Attr {
				if a.Key == "href" {
					links = append(links, a.Val)
				}
			}
		}
	}
	return links
}

func resolveUrl(baseURL, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("resolveUrl: %w", err)
	}

	if u.IsAbs() {
		return u.String(), nil
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("resolveUrl: %w", err)
	}

	abs := base.ResolveReference(u)
	return abs.String(), nil
}

func isValidURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	return supportedSchemes[parsed.Scheme]
}

func isInnerLink(checkedURL string, item AliveInnerLink) bool {
	// Парсим базовый URL
	baseParsed, err := url.Parse(item.URL)
	if err != nil {
		slog.Error("failed to parse base URL", "url", item.URL, "error", err)
		return false
	}

	// Парсим проверяемый URL
	checkedParsed, err := url.Parse(checkedURL)
	if err != nil {
		slog.Error("failed to parse checked URL", "url", checkedURL, "error", err)
		return false
	}

	// Если ссылка относительная - она внутренняя
	if !checkedParsed.IsAbs() {
		return true
	}

	// Сравниваем хосты
	return baseParsed.Host == checkedParsed.Host
}

func makeHEADorGETRequest(ctx context.Context, url string, opts Options) (*http.Response, error) {
	headReq, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return makeGetRequest(ctx, url, opts)
	}

	// Устанавливаем User-Agent (имитируем реальный браузер)
	headReq.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

	//headResp, err := opts.HTTPClient.Do(headReq)
	headResp, err := DoRequestWithRetries(headReq, opts)
	if err != nil {
		return makeGetRequest(ctx, url, opts)
	}

	// При успешном HEAD запросе возвращаем response
	if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
		return headResp, nil
	}
	//Если статус-код не успешный, то закрывем тело head-запроса и делаем get-запрос
	headResp.Body.Close()
	return makeGetRequest(ctx, url, opts)
}

func makeGetRequest(ctx context.Context, url string, opts Options) (*http.Response, error) {
	getReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("fail to create get request: %w", err)
	}

	getReq.Header.Set("User-Agent", opts.UserAgent)

	//getResp, err := opts.HTTPClient.Do(getReq)
	getResp, err := DoRequestWithRetries(getReq, opts)
	if err != nil {
		return nil, fmt.Errorf("fail to make get request: %w", err)
	}
	return getResp, nil
}

func CollectSEO(body io.Reader) (*SEO, error) {
	var seo SEO

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return &SEO{}, fmt.Errorf("goquery: %w", err)
	}

	title := doc.Find("title").Text()
	thereIsTitle := doc.Find("title").Length() > 0
	if thereIsTitle {
		seo.HasTitle = true
		seo.Title = html.UnescapeString(title)
	}

	dscr, exists := doc.Find("meta[name='description']").Attr("content")
	if exists {
		seo.HasDescription = true
		seo.Description = html.UnescapeString(dscr)
	}

	if exists := doc.Find("h1").Length() > 0; exists {
		seo.HasH1 = true
	}
	return &seo, nil
}

func ProcessLinks(links []string, opts *Options) ([]string, error) {
	repeated := make(map[string]struct{}) //отслеживаем одинаковые ссылки на странице
	URLs := make([]string, 0, len(links))
	for _, l := range links {
		_, ok := repeated[l]
		if ok {
			continue // если повторяется, то идем обрабатывать следующую ссылку
		}
		abs, err := resolveUrl(opts.URL, l) // преобразуем URLs в абсолютные
		if err != nil {
			slog.Warn("failed to resolve URL", "link", l, "error", err)
			continue
		}
		if abs == "" || !isValidURL(abs) {
			continue
		}

		URLs = append(URLs, abs)
		repeated[l] = struct{}{}
	}
	return URLs, nil
}

func ArrangeLinks(ctx context.Context, URLs []string, opts Options, item AliveInnerLink, queueCh chan AliveInnerLink,
	pendingURLs *int32, limiter *rate.Limiter) ([]BrokenLinks, error) {
	brLinks := make([]BrokenLinks, 0)

	for _, u := range URLs {
		if err := limiter.Wait(ctx); err != nil {
			slog.Error("rate limiter", "error", err)
			atomic.AddInt32(pendingURLs, -1)
			return brLinks, fmt.Errorf("rate limiter: %w", err)
		}
		resp, err := makeHEADorGETRequest(ctx, u, opts)
		if err != nil {
			//если возникает ошибка при обращении к url, то добавим url и ошибку в список битых ссылок
			brLinks = append(brLinks, BrokenLinks{
				URL: u,
				Err: err.Error(),
			})
			continue
		}
		resp.Body.Close()

		switch {
		case resp.StatusCode >= http.StatusBadRequest:
			brLinks = append(brLinks, BrokenLinks{
				URL:        u,
				StatusCode: resp.StatusCode,
			})

		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if isInnerLink(u, item) && item.LinkDepth < opts.Depth { //!!! было не верное условие и не выходил из цикла
				atomic.AddInt32(pendingURLs, 1)
				select {
				case queueCh <- AliveInnerLink{
					URL:       u,
					LinkDepth: item.LinkDepth + 1,
				}:
				default:
					// если буферизованный канал полон, то возвращаем счётчик обратно
					atomic.AddInt32(pendingURLs, -1)
					slog.Warn("queueCh is full")
				}
			}
		default:
			slog.Warn("unexpected StatusCode", "url", u, "StatusCode", resp.StatusCode)
		}
	}
	return brLinks, nil
}

func SetLimit(opts *Options) float64 {
	var limit float64
	switch {
	case opts.RPS != 0 && opts.Delay != 0:
		limit = float64(opts.RPS)

	case opts.RPS == 0 && opts.Delay != 0:
		str := opts.Delay.String()
		if strings.HasSuffix(str, "ms") {
			limit = float64(1000*time.Millisecond) / float64(opts.Delay)
		} else if strings.HasSuffix(str, "s") {
			limit = float64(time.Second) / float64(opts.Delay)
		}

	case opts.Delay == 0 && opts.RPS != 0:
		limit = float64(opts.RPS)
	default:
		limit = 0
	}
	return limit
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

func DoRequestWithRetries(req *http.Request, opts Options) (*http.Response, error) {
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
			resp.Body.Close()
		}

		time.Sleep(100 * time.Millisecond)
	}
	return resp, err
}

func CollectAssets(ctx context.Context, opts Options, baseURL string, body io.Reader, cache *AssetsCache) ([]Assets, error) {
	assets := make([]Assets, 0)

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return []Assets{}, fmt.Errorf("goquery: %w", err)
	}
	images := FindAssets(ctx, baseURL, opts, doc, cache, "img")
	assets = append(assets, images...)

	scripts := FindAssets(ctx, baseURL, opts, doc, cache, "script[src]")
	assets = append(assets, scripts...)

	styles := FindAssets(ctx, baseURL, opts, doc, cache, "link[rel='stylesheet']")
	assets = append(assets, styles...)

	return assets, nil
}

func FindAssets(ctx context.Context, baseURL string, opts Options,
	doc *goquery.Document, cache *AssetsCache, asset string) []Assets {
	var assets []Assets
	seen := make(map[string]bool)

	attrName := func(assetType string) string {
		switch assetType {
		case "img", "script[src]":
			return "src"
		case "link[rel='stylesheet']":
			return "href"
		default:
			return ""
		}
	}(asset)

	doc.Find(asset).Each(func(_ int, s *goquery.Selection) {
		if attrVal, exists := s.Attr(attrName); exists && attrVal != "" {
			u, err := resolveUrl(baseURL, attrVal)
			if err != nil {
				slog.Error("failed to resolve %s URL",
					"src", attrVal,
					"error", err)
				return
			}

			if seen[u] {
				return
			}
			seen[u] = true

			if cache.IsThereInCache(u) {
				assets = append(assets, cache.TakeFromCache(u))
				return
			}

			resp, err := makeGetRequest(ctx, u, opts)
			if err != nil {
				asset := Assets{
					URL:   u,
					Type:  determineAsset(asset),
					Error: err.Error(),
				}
				assets = append(assets, asset)
				cache.AddToCache(u, asset)
				return
			}

			if resp.StatusCode >= 400 {
				asset := Assets{
					URL:        u,
					Type:       determineAsset(asset),
					StatusCode: resp.StatusCode,
					Error:      resp.Status,
				}
				assets = append(assets, asset)
				cache.AddToCache(u, asset)
				return
			}

			size, err := FindOutContentLength(resp)
			if err != nil {
				slog.Debug("could not determine %s size",
					"url", u,
					"error", err)
			}

			if resp.Body != nil {
				resp.Body.Close()
			}

			asset := Assets{
				URL:        u,
				Type:       determineAsset(asset),
				StatusCode: resp.StatusCode,
				SizeBytes:  size,
			}
			assets = append(assets, asset)
			cache.AddToCache(u, asset)
		}
	})
	return assets
}

func FindOutContentLength(resp *http.Response) (int64, error) {
	if resp.Body == nil {
		return 0, errors.New("response body is nil")
	}

	if resp.ContentLength > 0 {
		return resp.ContentLength, nil
	}

	if resp.ContentLength == -1 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return 0, fmt.Errorf("failed to read body: %w", err)
		}
		size := int64(len(body))
		return size, nil
	}
	return 0, nil
}

func determineAsset(asset string) string {
	switch asset {
	case "img":
		return "image"
	case "script[src]":
		return "script"
	case "link[rel='stylesheet']":
		return "style"
	}
	return ""
}
