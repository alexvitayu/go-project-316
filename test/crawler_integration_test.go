package test

import (
	"bytes"
	"code/crawler"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestRoundTripper struct {
	data      []byte
	responses map[string]*http.Response
}

func NewTestResponse(path string, t *testing.T) *TestRoundTripper {
	d, err := os.ReadFile(path)
	require.NoError(t, err)
	return &TestRoundTripper{
		data:      d,
		responses: make(map[string]*http.Response),
	}
}

func (tr *TestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.String() {
	case "https://example.com":
		return &http.Response{
			Status:     "ok",
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(tr.data)),
		}, nil
	case "https://example.com/missing":
		return &http.Response{
			Status:     "Not Found",
			StatusCode: 404,
		}, nil
	case "https://example.com/static/logo.png":
		return &http.Response{
			StatusCode:    200,
			ContentLength: 12345,
			Body:          io.NopCloser(strings.NewReader("len body = 12345")),
		}, nil
	default:
		return &http.Response{
			StatusCode: 0,
		}, errors.New("Unknown URL")
	}
}

func TestCrawler(t *testing.T) {
	t.Parallel()
	rt := NewTestResponse("./testdata/example.html", t)

	client := &http.Client{
		Transport: rt, // вместо реального похода в интернет, вернутся моковые ответы
	}

	opts := crawler.Options{
		URL:         "https://example.com",
		Depth:       1,
		Retries:     2,
		Delay:       0 * time.Microsecond,
		Timeout:     30000 * time.Second,
		Concurrency: 2,
		IndentJSON:  false,
		HTTPClient:  client,
	}

	exp, err := os.ReadFile("./testdata/fixture.json")
	require.NoError(t, err)
	var expect crawler.Report
	if jsErr := json.Unmarshal(exp, &expect); jsErr != nil {
		t.Log("fail to unmarshal expect", "error", err)
	}
	expect.GeneratedAt = ""
	expect.Pages[0].DiscoveredAt = ""

	res, err := crawler.Analyze(t.Context(), opts)
	require.NoError(t, err)
	var result crawler.Report
	if jErr := json.Unmarshal(res, &result); jErr != nil {
		t.Log("fail to unmarshal result", "error", err)
	}
	result.GeneratedAt = ""
	result.Pages[0].DiscoveredAt = ""

	assert.Equal(t, expect, result)
}
