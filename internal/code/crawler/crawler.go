package crawler

import (
	"context"
	"encoding/json"
	"fmt"

	"net/http"

	"time"
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
	URL        string `json:"url"`
	Depth      int    `json:"depth"`
	HTTPStatus int    `json:"http_status"`
	Status     string `json:"status"`
	Error      string `json:"error"`
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

	var page Page
	resp, err := opts.HTTPClient.Get(opts.URL)
	if err != nil {
		//page = Page{URL: opts.URL,
		//	Depth:      opts.Depth,
		//	HTTPStatus: 0,
		//	Status:     "Failed",
		//	Error:      "get request failed",
		//}
		return nil, fmt.Errorf("get request failed: %w", err)
	}
	if resp != nil {
		defer resp.Body.Close()
	}

	page = Page{
		URL:        opts.URL,
		Depth:      opts.Depth,
		HTTPStatus: resp.StatusCode,
		Status:     resp.Status,
		Error:      "",
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
