package tools

import (
	"code/internal/models"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

var supportedSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

func NormalizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.Fragment = ""

	parsed.Path = strings.TrimSuffix(parsed.Path, "/")

	return parsed.String()
}

func ProcessLinks(links []string, baseURL string) ([]string, error) {
	repeated := make(map[string]struct{}) //отслеживаем одинаковые ссылки на странице
	URLs := make([]string, 0, len(links))
	for _, l := range links {
		abs, err := ResolveUrl(baseURL, l) // преобразуем URLs в абсолютные
		if err != nil {
			slog.Warn("failed to resolve URL", "link", l, "error", err)
			continue
		}
		if abs == "" || !isValidURL(abs) {
			continue
		}

		normalized := NormalizeURL(abs)

		// Проверяем, не было ли уже нормализованной версии
		if _, ok := repeated[normalized]; ok {
			continue
		}

		URLs = append(URLs, normalized)
		repeated[normalized] = struct{}{}
	}
	return URLs, nil
}

func IsInnerLink(checkedURL string, item models.AliveInnerLink) bool {
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
	// Нормализуем оба URL для сравнения
	normalizedBase := NormalizeURL(item.URL)
	normalizedChecked := NormalizeURL(checkedURL)

	// Если это та же страница после нормализации - не внутренняя ссылка
	if normalizedBase == normalizedChecked {
		return false
	}

	// Если разные хосты - внешняя ссылка
	if baseParsed.Host != checkedParsed.Host {
		return false
	}

	// Если ссылка относительная или тот же хост - внутренняя
	return true
}

func ResolveUrl(baseURL, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("ResolveUrl: %w", err)
	}

	if u.IsAbs() {
		return u.String(), nil
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("ResolveUrl: %w", err)
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
