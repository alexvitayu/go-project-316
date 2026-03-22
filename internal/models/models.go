package models

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
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
	HTTPClient  HTTPClient
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
	StatusCode int    `json:"status_code,omitempty"`
	Err        string `json:"error,omitempty"`
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

type FetchCrawlParams struct {
	Ctx         context.Context
	QueueCh     chan AliveInnerLink
	Done        chan<- struct{}
	ErrsCh      chan<- error
	PagesCh     chan<- Page
	Index       int
	PendingURLs *int32
	Options     Options
	Visits      *Visits
	Limiter     *rate.Limiter
	Cache       *AssetsCache
	URLs        []string
	Item        AliveInnerLink
	BrLinks     *[]BrokenLinks
	Client      HTTPClient
}

type FetchCollectParams struct {
	Ctx     context.Context
	Opts    Options
	BaseURL string
	Body    io.Reader
	Cache   *AssetsCache
	Client  HTTPClient
	Doc     *goquery.Document
}

type Assets struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	StatusCode int    `json:"status_code"`
	SizeBytes  int64  `json:"size_bytes"`
	Error      string `json:"error,omitempty"`
}

type SEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

type Visits struct {
	Mu        *sync.Mutex
	IsVisited map[string]struct{}
}

func NewVisits() *Visits {
	return &Visits{
		IsVisited: make(map[string]struct{}),
		Mu:        &sync.Mutex{},
	}
}

type AssetsCache struct {
	Cache map[string]Assets
	Mu    *sync.Mutex
}

func NewCacheAssets() *AssetsCache {
	return &AssetsCache{
		Cache: make(map[string]Assets),
		Mu:    &sync.Mutex{},
	}
}

func (c *AssetsCache) AddToCache(url string, assets Assets) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	if _, exists := c.Cache[url]; !exists {
		c.Cache[url] = assets
	}
}

func (c *AssetsCache) IsThereInCache(url string) bool {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	_, ok := c.Cache[url]
	return ok
}

func (c *AssetsCache) TakeFromCache(url string) Assets {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	asset, exists := c.Cache[url]
	if exists {
		return asset
	}
	return Assets{}
}
