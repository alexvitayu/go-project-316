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
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

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

// Analyze - Основная точка входа в crawler
func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	_ = ctx //TODO
	var pages []Page

	// Делаем запрос на базовый URL get запросом для извлечения тела - html
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
			resp.Body.Close() // Даже при ошибке resp может быть не nil!
		}
		return nil, fmt.Errorf("get request failed: %w", err)
	}

	// Сохраним body для дальнейшей работы в разных местах
	savedBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to save body: %w", err)
	}

	// Извлекаем ссылки из стартовой страницы
	links := checkHTML(bytes.NewReader(savedBody))

	// Преобразуем ссылки в абсолютные url и убираем дублирующиеся
	URLs, err := ProcessLinks(links, &opts)
	if err != nil {
		return nil, fmt.Errorf("ProcessLinks: %w", err)
	}

	// Сделаем запросы к найденным URL и сформируем слайс битых ссылок
	brLinks, err := FindBrokenLinks(URLs, opts)
	if err != nil {
		return nil, fmt.Errorf("FindBrokenLinks: %w", err)
	}

	// Соберём SEO из полученной html страницы
	seo, err := CollectSEO(bytes.NewReader(savedBody))
	if err != nil {
		return nil, fmt.Errorf("CollectSEO: %w", err)
	}

	page := Page{
		URL:          opts.URL,
		Depth:        opts.Depth,
		HTTPStatus:   resp.StatusCode,
		Status:       resp.Status,
		Error:        "",
		Seo:          seo,
		BrokenLinks:  brLinks,
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

func FindBrokenLinks(URLs []string, opts Options) ([]BrokenLinks, error) {
	var brLinks []BrokenLinks
	var brLink BrokenLinks

	for _, u := range URLs {
		resp, err := makeHEADorGETRequest(u, opts)
		if err != nil {
			//если возникает ошибка при обращении к url, то добавим url и ошибку в список битых ссылок
			brLink = BrokenLinks{
				URL: u,
				Err: err.Error(),
			}
			brLinks = append(brLinks, brLink)
			continue
		}

		if resp.StatusCode >= 400 {
			brLink = BrokenLinks{
				URL:        u,
				StatusCode: resp.StatusCode,
			}
			brLinks = append(brLinks, brLink)
		}
		resp.Body.Close()
	}
	return brLinks, nil
}

func makeHEADorGETRequest(url string, opts Options) (*http.Response, error) {
	headReq, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return makeGetRequest(url, opts)
	}

	// Устанавливаем User-Agent (имитируем реальный браузер)
	headReq.Header.Set("User-Agent", opts.UserAgent) //обход блокировок на некоторых сайтах

	headResp, err := opts.HTTPClient.Do(headReq)
	if err != nil {
		return makeGetRequest(url, opts)
	}

	// При успешном HEAD запросе возвращаем response
	if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
		return headResp, nil
	}
	//Если статус-код не успешный, то закрывем тело head-запроса и делаем get-запрос
	headResp.Body.Close()
	return makeGetRequest(url, opts)
}

func makeGetRequest(url string, opts Options) (*http.Response, error) {
	getReq, err := http.NewRequest("GET", url, nil)
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
