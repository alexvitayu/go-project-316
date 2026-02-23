package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

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
	BrokenLinks  []BrokenLinks `json:"broken_links"`
	Error        string        `json:"error"`
	DiscoveredAt time.Time     `json:"discovered_at,omitempty"`
}

type BrokenLinks struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Err        string `json:"error,omitempty"`
}

type Response struct {
	RootURL     string    `json:"root_url"`
	Depth       int       `json:"depth"`
	GeneratedAt time.Time `json:"generated_at"`
	Pages       []Page    `json:"pages"`
}

// Analyze - Основная точка входа в crawler
func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	_ = ctx //TODO
	var pages []Page

	fmt.Println("печатаю текст...")               //TODO
	fmt.Printf("User-Agent = %s", opts.UserAgent) //TODO

	// Делаем запрос на базовый URL
	// Создаем новый запрос
	req, err := http.NewRequest("GET", opts.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}

	// Устанавливаем User-Agent (имитируем реальный браузер)
	req.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

	// Выполняем запрос
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close() // Даже при ошибке resp может быть не nil!
		}
		return nil, fmt.Errorf("get request failed: %w", err)
	}
	defer resp.Body.Close()

	// Извлекаем ссылки из стартовой страницы
	links := checkHTML(resp.Body)

	for i, v := range links {
		n := i + 1
		fmt.Printf("link %d = %s\n", n, v)
	}

	// Преобразуем ссылки в абсолютные url и убираем дублирующиеся
	repeated := make(map[string]struct{}) //отслеживаем одинаковые ссылки на странице
	URLs := make([]string, 0, len(links))
	for _, l := range links {
		_, ok := repeated[l]
		if ok {
			continue // если повторяется, то идем обрабатывать следующую ссылку
		}
		abs := resolveUrl(opts.URL, l)
		URLs = append(URLs, abs)
		repeated[l] = struct{}{}
	}

	for i, v := range URLs {
		n := i + 1
		fmt.Printf("link %d = %s\n", n, v)
	}

	// Сделаем запросы к найденным URL и сформируем слайс битых ссылок
	brLinks, err := findBrokenLinks(URLs, opts)
	if err != nil {
		return nil, fmt.Errorf("findBrokenLinks: %w", err)
	}

	page := Page{
		URL:          opts.URL,
		Depth:        opts.Depth,
		HTTPStatus:   resp.StatusCode,
		Status:       resp.Status,
		BrokenLinks:  brLinks,
		Error:        "",
		DiscoveredAt: time.Now(),
	}

	pages = append(pages, page)

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

func resolveUrl(baseURL, rawURL string) string {
	u, err := url.Parse(rawURL)
	_ = err //TODO

	if u != nil {
		if u.IsAbs() {
			return u.String()
		}
	}

	base, _ := url.Parse(baseURL)
	_ = err //TODO

	abs := base.ResolveReference(u)
	return abs.String()
}

//func resolveUrl(baseURL, rawURL string) string {
//	// Пропускаем пустые ссылки
//	if strings.TrimSpace(rawURL) == "" {
//		return ""
//	}
//
//	// Специальные протоколы, которые не нужно резолвить
//	if strings.HasPrefix(rawURL, "mailto:") ||
//		strings.HasPrefix(rawURL, "tel:") ||
//		strings.HasPrefix(rawURL, "javascript:") {
//		return rawURL
//	}
//
//	// Якоря (если хотите обрабатывать отдельно)
//	if strings.HasPrefix(rawURL, "#") {
//		return baseURL + rawURL // или просто rawURL, в зависимости от задачи
//	}
//
//	// Парсим rawURL
//	u, err := url.Parse(rawURL)
//	if err != nil || u == nil {
//		// Если не удалось распарсить, возвращаем исходную строку
//		return rawURL
//	}
//
//	// Если это абсолютный URL, возвращаем как есть
//	if u.IsAbs() {
//		return u.String()
//	}
//
//	// Парсим базовый URL
//	base, err := url.Parse(baseURL)
//	if err != nil || base == nil {
//		// Если базовый URL некорректный, возвращаем rawURL
//		return rawURL
//	}
//
//	// Разрешаем относительный URL
//	resolved := base.ResolveReference(u)
//	if resolved == nil {
//		return rawURL
//	}
//
//	return resolved.String()
//}

func findBrokenLinks(URLs []string, opts Options) ([]BrokenLinks, error) {
	var brLinks []BrokenLinks
	var brLink BrokenLinks
	for _, u := range URLs {

		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to make request: %w", err)
		}

		// Устанавливаем User-Agent (имитируем реальный браузер)
		req.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

		// Выполняем запрос
		resp, err := opts.HTTPClient.Do(req)
		if err != nil {
			if resp != nil {
				defer resp.Body.Close() // Даже при ошибке resp может быть не nil!
			}
			//если возникает ошибка при обращении к url, то добавим url и ошибку в список битых ссылок
			brLink = BrokenLinks{
				URL: u,
				Err: fmt.Sprint(err),
			}
			brLinks = append(brLinks, brLink)
			return nil, fmt.Errorf("get request failed: %w", err)
		}
		defer resp.Body.Close()

		if strings.HasPrefix(strconv.Itoa(resp.StatusCode), "4") ||
			strings.HasPrefix(strconv.Itoa(resp.StatusCode), "5") {
			brLink = BrokenLinks{
				URL:        u,
				StatusCode: resp.StatusCode,
			}
			brLinks = append(brLinks, brLink)
		}
	}
	return brLinks, nil
}
