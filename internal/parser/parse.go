package parser

import (
	"code/internal/cache/assetscache"
	"code/internal/fetcher"
	"code/internal/models"
	"code/internal/tools"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type FetchCollectParams struct {
	Opts    models.Options
	BaseURL string
	Body    io.Reader
	Cache   *assetscache.AssetsCache
	Client  http.Client
	Doc     *goquery.Document
}

func ParseHTML(r io.Reader) []string {
	var links []string

	doc, err := html.Parse(r)
	if err != nil {
		slog.Error("ParseHTML", "parse html failed", err)
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

func CollectAssets(ctx context.Context, p FetchCollectParams) ([]models.Assets, error) {
	assets := make([]models.Assets, 0)

	doc, err := goquery.NewDocumentFromReader(p.Body)
	if err != nil {
		return []models.Assets{}, fmt.Errorf("goquery: %w", err)
	}
	p.Doc = doc
	images := FindAssets(ctx, p, "img")
	assets = append(assets, images...)

	scripts := FindAssets(ctx, p, "script[src]")
	assets = append(assets, scripts...)

	styles := FindAssets(ctx, p, "link[rel='stylesheet']")
	assets = append(assets, styles...)

	return assets, nil
}

func FindAssets(ctx context.Context, p FetchCollectParams, asset string) []models.Assets {
	var assets []models.Assets
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

	p.Doc.Find(asset).Each(func(_ int, s *goquery.Selection) {
		if attrVal, exists := s.Attr(attrName); exists && attrVal != "" {
			u, err := tools.ResolveUrl(p.BaseURL, attrVal)
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

			if p.Cache.IsThereInCache(u) {
				assets = append(assets, p.Cache.TakeFromCache(u))
				return
			}

			resp, err := fetcher.MakeGetRequest(ctx, u, &p.Opts)
			if err != nil {
				asset := models.Assets{
					URL:   u,
					Type:  determineAsset(asset),
					Error: err.Error(),
				}
				assets = append(assets, asset)
				p.Cache.AddToCache(u, asset)
				return
			}

			if resp.StatusCode >= 400 {
				asset := models.Assets{
					URL:        u,
					Type:       determineAsset(asset),
					StatusCode: resp.StatusCode,
					Error:      resp.Status,
				}
				assets = append(assets, asset)
				p.Cache.AddToCache(u, asset)
				return
			}

			size, err := FindOutContentLength(resp)
			if err != nil {
				slog.Debug("could not determine %s size",
					"url", u,
					"error", err)
			}

			if resp.Body != nil {
				if err := resp.Body.Close(); err != nil {
					slog.Debug("failed to close asset response body",
						"url", u,
						"error", err)
				}
			}

			asset := models.Assets{
				URL:        u,
				Type:       determineAsset(asset),
				StatusCode: resp.StatusCode,
				SizeBytes:  size,
			}
			assets = append(assets, asset)
			p.Cache.AddToCache(u, asset)
		}
	})
	return assets
}

func CollectSEO(body io.Reader) (*models.SEO, error) {
	seo := models.SEO{}

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return &models.SEO{}, fmt.Errorf("goquery: %w", err)
	}

	title := doc.Find("title").First().Text()
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
