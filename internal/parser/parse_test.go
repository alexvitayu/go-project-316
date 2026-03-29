package parser

import (
	"code/internal/cache/assetscache"
	"code/internal/models"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectSEO(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name    string
		handler http.Handler
		wantSEO *models.SEO
	}{
		{
			name: "seo_positive_test",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.ServeFile(writer, request, "./../../testdata/seo_test/positive_test.html")
			}),
			wantSEO: &models.SEO{
				HasTitle:       true,
				Title:          "My first site",
				HasDescription: true,
				Description:    "Programing & learning",
				HasH1:          true,
			},
		},
		{
			name: "seo_negative_test",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.ServeFile(writer, request, "./../../testdata/seo_test/negative_test.html")
			}),
			wantSEO: &models.SEO{
				HasTitle:       false,
				Title:          "",
				HasDescription: false,
				Description:    "",
				HasH1:          false,
			},
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(tc.handler)
			client := http.Client{}
			resp, err := client.Get(server.URL)
			require.NoError(t, err)
			seo, err := CollectSEO(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.wantSEO, seo)
			if err := resp.Body.Close(); err != nil {
				t.Logf("failed to close response body: %v", err)
			}
		})
	}
}

type RoundTripCounter struct {
	count int32
}

func (rtc *RoundTripCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	rtc.count++
	return &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`<!DOCTYPE html>
<html>
<head>
  <title>Тест дублирования ассетов</title>
</head>
<body>
<h1>Тестовая страница с дублирующимся ассетом</h1>

<!-- Один и тот же ассет используется дважды -->
<img src="/images/test-image.jpg" alt="Тестовое изображение 1">
<img src="/images/test-image.jpg" alt="Тестовое изображение 2">

<p>На этой странице одно и то же изображение используется дважды.</p>
</body>
</html>`)),

		Header:  http.Header{"Content-Type": []string{"text/html"}},
		Request: req,
	}, nil
}

func TestCollectAssets_RepeatedAssets(t *testing.T) {
	t.Parallel()

	cache := assetscache.NewCacheAssets()

	testTransport := &RoundTripCounter{}

	client := &http.Client{
		Transport: testTransport,
	}

	req, err := http.NewRequest("GET", "http://example.com", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() {
		if err = resp.Body.Close(); err != nil {
			t.Logf("failed to close response body: %v", err)
		}
	}()

	p := FetchCollectParams{
		BaseURL: "http://example.com",
		Body:    resp.Body,
		Cache:   cache,
		Client:  *client,
	}

	_, err = CollectAssets(t.Context(), p)
	require.NoError(t, err)

	require.Equal(t, int32(2), testTransport.count)

	exists := cache.IsThereInCache("http://example.com/images/test-image.jpg")
	require.True(t, exists)
	require.Equal(t, "image", cache.TakeFromCache("http://example.com/images/test-image.jpg").Type)
}

func TestFindOutContentLength(t *testing.T) {
	t.Parallel()

	var tt = []struct {
		name       string
		resp       *http.Response
		wantLength int64
		isThereErr bool
		err        string
	}{
		{name: "error_body_is_nil",
			resp:       &http.Response{},
			isThereErr: true,
			err:        "response body is nil",
		},
		{name: "positive_content_length",
			resp: &http.Response{
				ContentLength: 777,
				Body:          io.NopCloser(strings.NewReader("content_length_is_777")),
			},
			wantLength: int64(777),
			isThereErr: false,
		},
		{name: "content_length_is_-1",
			resp: &http.Response{
				ContentLength: -1,
				Body:          io.NopCloser(strings.NewReader("content_length_is_20")),
			},
			wantLength: int64(20),
			isThereErr: false,
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			size, err := FindOutContentLength(tc.resp)
			if tc.isThereErr {
				require.Error(t, err)
				assert.Equal(t, tc.err, err.Error())
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantLength, size)
			}
		})
	}
}

func TestFindAssets(t *testing.T) {
	t.Parallel()
	cache := assetscache.NewCacheAssets()
	expectedAsset := models.Assets{
		URL:        "http://example.com/images/test-image.jpg",
		Type:       "image",
		StatusCode: 404,
		SizeBytes:  0,
		Error:      "404 Not Found",
	}
	client := &http.Client{}

	body := strings.NewReader(`<img src="/images/test-image.jpg" alt="Тестовое изображение 1">`)

	doc, err := goquery.NewDocumentFromReader(body)
	require.NoError(t, err)

	p := FetchCollectParams{
		BaseURL: "http://example.com",
		Body:    body,
		Cache:   cache,
		Client:  *client,

		Doc: doc,
	}
	assets := FindAssets(t.Context(), p, "img")

	assert.Equal(t, expectedAsset, assets[0])
}
