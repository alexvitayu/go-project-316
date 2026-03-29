package models

type Page struct {
	URL          string       `json:"url"`
	Depth        int          `json:"depth"`
	HTTPStatus   int          `json:"http_status"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
	SEO          *SEO         `json:"seo"`
	BrokenLinks  []BrokenLink `json:"broken_links"`
	Assets       []Assets     `json:"assets"`
	DiscoveredAt string       `json:"discovered_at"`
}

type BrokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Err        string `json:"error,omitempty"`
}

type AliveInnerLink struct {
	URL       string
	LinkDepth int
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
