package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
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
	IndentJSON  string
	HTTPClient  *http.Client
}

type Page struct {
	URL          string        `json:"url"`
	Depth        int           `json:"depth"`
	HTTPStatus   int           `json:"http_status"`
	Status       string        `json:"status"`
	Error        string        `json:"error"`
	Seo          *Seo          `json:"seo"`
	BrokenLinks  []BrokenLinks `json:"broken_links"`
	DiscoveredAt time.Time     `json:"discovered_at,omitempty"`
}

type BrokenLinks struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Err        string `json:"error,omitempty"`
}

type Seo struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

type Response struct {
	RootURL     string    `json:"root_url"`
	Depth       int       `json:"depth"`
	GeneratedAt time.Time `json:"generated_at"`
	Pages       []Page    `json:"pages"`
}

type AliveInnerLink struct {
	URL       string
	LinkDepth int
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

	visits := NewVisits()

	// Для орагизации извлечения ссылок и дальнейшей их обработки создадим очередь
	//queue := make([]AliveInnerLink, 0, initQueueCapacity)

	queueCh := make(chan AliveInnerLink, initQueueCapacity)

	errsCh := make(chan error, opts.Concurrency*2)

	pagesCh := make(chan Page, 100)

	var wg sync.WaitGroup

	// Это старт очереди с глубиной 0
	//queue = append(queue, AliveInnerLink{
	//	URL:       opts.URL,
	//	LinkDepth: 0,
	//})

	queueCh <- AliveInnerLink{
		URL:       opts.URL,
		LinkDepth: 0,
	}

	for i := 1; i <= opts.Concurrency; i++ {
		wg.Add(1)
		go func(wg *sync.WaitGroup, indx int) {
			defer wg.Done()
			fmt.Printf("горутина %d начала работу\n", indx)
			for item := range queueCh {
				var page Page

				if ctx.Err() != nil { // истёк таймаут или произошла отмена Ctrl + C
					errsCh <- fmt.Errorf("timeout: %w", ctx.Err())
					break
				}

				if item.LinkDepth <= opts.Depth {

					visits.mu.Lock()
					if _, ok := visits.isVisited[item.URL]; ok {
						visits.mu.Unlock()
						continue
					}
					visits.isVisited[item.URL] = struct{}{}
					visits.mu.Unlock()

					// Создаем новый запрос
					req, err := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
					if err != nil {
						page.Error = err.Error()
						errsCh <- fmt.Errorf("failed to make request: %w", err)
						continue
					}

					// Устанавливаем User-Agent (имитируем реальный браузер)
					req.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

					// Выполняем запрос
					resp, err := opts.HTTPClient.Do(req)
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
						errsCh <- fmt.Errorf("failed to save body: %w", err)
						continue
					}

					// Извлекаем ссылки из страницы
					links := checkHTML(bytes.NewReader(savedBody))

					// Преобразуем ссылки в абсолютные url и убираем дублирующиеся
					URLs, err := ProcessLinks(links, &opts)
					if err != nil {
						errsCh <- fmt.Errorf("ProcessLinks: %w", err)
						continue
					}

					// Сделаем запросы к найденным URL и сформируем слайс битых ссылок
					//brLinks, err := ArrangeLinks(ctx, URLs, opts, item, &queue)
					//if err != nil {
					//	page.Error = err.Error()
					//	return nil, fmt.Errorf("ArrangeLinks: %w", err)
					//}

					var brLinks []BrokenLinks

					for _, u := range URLs {
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
								//*queue = append(*queue, AliveInnerLink{
								//	URL:       u,
								//	LinkDepth: item.LinkDepth + 1,
								//})
								queueCh <- AliveInnerLink{
									URL:       u,
									LinkDepth: item.LinkDepth + 1,
								}
							}
						default:
							slog.Warn("unexpected StatusCode", "url", u, "StatusCode", resp.StatusCode)
						}
					}

					// Соберём SEO из полученной html страницы
					seo, err := CollectSEO(bytes.NewReader(savedBody))
					if err != nil {
						page.Error = err.Error()
						errsCh <- fmt.Errorf("CollectSEO: %w", err)
						continue
					}

					page.URL = item.URL
					page.Depth = item.LinkDepth
					page.HTTPStatus = resp.StatusCode
					page.Status = resp.Status
					page.Seo = seo
					page.BrokenLinks = brLinks
					page.DiscoveredAt = time.Now()

					pagesCh <- page
				}
			}
			fmt.Printf("горутина %d завершила работу\n", indx)
		}(&wg, i)
	}

	go func() {
		wg.Wait()
		fmt.Println("все горутины завершили работу")
		close(queueCh)
		close(errsCh)
		close(pagesCh)
		fmt.Println("все каналы закрыты")
	}()

	var pages []Page
	var errs []error
	var mywg sync.WaitGroup

	mywg.Add(1)
	go func(ps *[]Page, errs *[]error, wg *sync.WaitGroup) {
		defer wg.Done()
	Label:
		for {
			select {
			case p, ok := <-pagesCh:
				if !ok {
					break Label
				}
				*ps = append(*ps, p)
				fmt.Printf("получена страница = %v\n", p.URL)
			case err, ok := <-errsCh:
				if !ok {
					break Label
				}
				*errs = append(*errs, err)
			case <-ctx.Done():
				break Label
			default:

			}
		}
	}(&pages, &errs, &mywg)

	mywg.Wait()

	data := Response{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now(),
		Pages:       pages,
	}

	report, err := json.MarshalIndent(data, "", opts.IndentJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal report: %w", err)
	}
	return report, nil
}

//
//func worker(ctx context.Context, queue []AliveInnerLink, pages []Page, opts Options, visits *Visits) ([]Page, error) {
//	for len(queue) > 0 {
//		var page Page
//		if ctx.Err() != nil { // истёк таймаут или произошла отмена Ctrl + C
//			page.Error = ctx.Err().Error()
//			pages = append(pages, page)
//			break
//		}
//		item := queue[0]
//
//		if item.LinkDepth <= opts.Depth {
//			queue = queue[1:]
//
//			visits.mu.Lock()
//			if _, ok := visits.isVisited[item.URL]; ok {
//				visits.mu.Unlock()
//				continue
//			}
//			visits.isVisited[item.URL] = struct{}{}
//			visits.mu.Unlock()
//
//			// Создаем новый запрос
//			req, err := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
//			if err != nil {
//				page.Error = err.Error()
//				return nil, fmt.Errorf("failed to make request: %w", err)
//			}
//
//			// Устанавливаем User-Agent (имитируем реальный браузер)
//			req.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах
//
//			// Выполняем запрос
//			resp, err := opts.HTTPClient.Do(req)
//			if err != nil {
//				page.Error = err.Error()
//				if resp != nil {
//					resp.Body.Close() // Даже при ошибке resp может быть не nil!
//				}
//				return nil, fmt.Errorf("get request failed: %w", err)
//			}
//
//			// Сохраним body для дальнейшей работы в разных местах
//			savedBody, err := io.ReadAll(resp.Body)
//			resp.Body.Close()
//			if err != nil {
//				page.Error = err.Error()
//				return nil, fmt.Errorf("failed to save body: %w", err)
//			}
//
//			// Извлекаем ссылки из страницы
//			links := checkHTML(bytes.NewReader(savedBody))
//
//			// Преобразуем ссылки в абсолютные url и убираем дублирующиеся
//			URLs, err := ProcessLinks(links, &opts)
//			if err != nil {
//				page.Error = err.Error()
//				return nil, fmt.Errorf("ProcessLinks: %w", err)
//			}
//
//			// Сделаем запросы к найденным URL и сформируем слайс битых ссылок
//			brLinks, err := ArrangeLinks(ctx, URLs, opts, item, &queue)
//			if err != nil {
//				page.Error = err.Error()
//				return nil, fmt.Errorf("ArrangeLinks: %w", err)
//			}
//
//			// Соберём SEO из полученной html страницы
//			seo, err := CollectSEO(bytes.NewReader(savedBody))
//			if err != nil {
//				page.Error = err.Error()
//				return nil, fmt.Errorf("CollectSEO: %w", err)
//			}
//
//			page.URL = item.URL
//			page.Depth = item.LinkDepth
//			page.HTTPStatus = resp.StatusCode
//			page.Status = resp.Status
//			page.Seo = seo
//			page.BrokenLinks = brLinks
//			page.DiscoveredAt = time.Now()
//
//			pages = append(pages, page)
//		}
//	}
//	return pages, nil
//}

func checkHTML(r io.Reader) []string {
	var links []string

	doc, _ := html.Parse(r)
	//err TODO

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

//
//func ArrangeLinks(ctx context.Context, URLs []string, opts Options, item AliveInnerLink, queue *[]AliveInnerLink) ([]BrokenLinks, error) {
//	var brLinks []BrokenLinks
//
//	for _, u := range URLs {
//		resp, err := makeHEADorGETRequest(ctx, u, opts)
//		if err != nil {
//			//если возникает ошибка при обращении к url, то добавим url и ошибку в список битых ссылок
//			brLinks = append(brLinks, BrokenLinks{
//				URL: u,
//				Err: err.Error(),
//			})
//			continue
//		}
//		resp.Body.Close()
//
//		switch {
//		case resp.StatusCode >= http.StatusBadRequest:
//			brLinks = append(brLinks, BrokenLinks{
//				URL:        u,
//				StatusCode: resp.StatusCode,
//			})
//
//		case resp.StatusCode >= 200 && resp.StatusCode < 300:
//			if isInnerLink(u, item) && item.LinkDepth < opts.Depth { //!!! было не верное условие и не выходил из цикла
//				*queue = append(*queue, AliveInnerLink{
//					URL:       u,
//					LinkDepth: item.LinkDepth + 1,
//				})
//			}
//		default:
//			slog.Warn("unexpected StatusCode", "url", u, "StatusCode", resp.StatusCode)
//		}
//	}
//	return brLinks, nil
//}

func makeHEADorGETRequest(ctx context.Context, url string, opts Options) (*http.Response, error) {
	headReq, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return makeGetRequest(ctx, url, opts)
	}

	// Устанавливаем User-Agent (имитируем реальный браузер)
	headReq.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

	headResp, err := opts.HTTPClient.Do(headReq)
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

	getResp, err := opts.HTTPClient.Do(getReq)
	if err != nil {
		return nil, fmt.Errorf("fail to make get request: %w", err)
	}
	return getResp, nil
}

func CollectSEO(body io.Reader) (*Seo, error) {
	var seo Seo

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return &Seo{}, fmt.Errorf("goquery: %w", err)
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
			slog.Warn("failed to resolve URL %s: %v\n", l, err)
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
