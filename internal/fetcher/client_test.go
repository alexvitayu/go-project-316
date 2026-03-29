package fetcher

import (
	"code/internal/cache/linkscache"
	"code/internal/models"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestArrangeLinks(t *testing.T) {
	t.Run("find_broken_links", func(t *testing.T) {
		sourceLinks := []string{"https://www.google.com", "https://github.com",
			"https://www.google.com/non-existent-page", "https://wooordhunt.ru/word/хтрй"}

		item := models.AliveInnerLink{}
		queueCh := make(chan models.AliveInnerLink)
		limiter := rate.NewLimiter(rate.Inf, 1)
		wantLengthBroken := 2
		brLinks := make([]models.BrokenLink, 0)
		linksCache := linkscache.NewLinksCache()

		p := FetchCrawlParams{
			URLs:       sourceLinks,
			Item:       item,
			QueueCh:    queueCh,
			Limiter:    limiter,
			BrLinks:    &brLinks,
			Client:     &http.Client{},
			UserAgent:  "curl/8.14.1",
			LinksCache: linksCache,
		}

		err := ArrangeLinks(t.Context(), p)
		require.NoError(t, err)
		assert.Len(t, brLinks, wantLengthBroken)
	})
}
