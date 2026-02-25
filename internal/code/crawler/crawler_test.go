package crawler_test

import (
	"code/internal/code/crawler"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyze(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name         string
		handler      http.HandlerFunc
		options      func(server *httptest.Server) crawler.Options
		wantResponse crawler.Response
		wantErr      error
		thereIsErr   bool
	}{
		{
			name: "200_response",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				response := crawler.Response{
					RootURL: r.Host, //или server.URL
					Pages: []crawler.Page{
						{
							URL:        r.Host,
							HTTPStatus: http.StatusOK,
							Status:     "200 OK",
							Error:      "",
						},
					},
				}
				err := json.NewEncoder(w).Encode(response)
				assert.NoError(t, err)
			}),
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:        server.URL,
					HTTPClient: &http.Client{},
				}
			},
			wantResponse: crawler.Response{
				Pages: []crawler.Page{
					{
						HTTPStatus: http.StatusOK,
						Status:     "200 OK",
						Error:      "",
					},
				},
			},
			wantErr:    nil,
			thereIsErr: false,
		},
		{
			name: "404_Not_Found",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Header().Set("Content-Type", "application/json")
				response := crawler.Response{
					RootURL: r.Host, //или server.URL
					Pages: []crawler.Page{
						{
							URL:        r.Host,
							HTTPStatus: http.StatusNotFound,
							Status:     "404 Not Found",
							Error:      "",
						},
					},
				}
				err := json.NewEncoder(w).Encode(response)
				assert.NoError(t, err)
			}),
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:        server.URL,
					HTTPClient: &http.Client{},
				}
			},
			wantResponse: crawler.Response{
				Pages: []crawler.Page{
					{
						HTTPStatus: http.StatusNotFound,
						Status:     "404 Not Found",
						Error:      "",
					},
				},
			},
			wantErr:    nil,
			thereIsErr: false,
		},
		{
			name: "700_invalid_status",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(700)
				w.Header().Set("Content-Type", "application/json")
				response := crawler.Response{
					RootURL: r.Host, //или server.URL
					Pages: []crawler.Page{
						{
							URL:        r.Host,
							HTTPStatus: 700,
							Error:      "",
						},
					},
				}
				err := json.NewEncoder(w).Encode(response)
				assert.NoError(t, err)
			}),
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:        server.URL,
					HTTPClient: &http.Client{},
				}
			},
			wantResponse: crawler.Response{
				Pages: []crawler.Page{
					{
						HTTPStatus: 700,
						Error:      "",
					},
				},
			},
			wantErr:    nil,
			thereIsErr: false,
		},
		{
			name: "timeout_exceeded",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(5 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			},
			options: func(server *httptest.Server) crawler.Options {
				return crawler.Options{
					URL: server.URL,
					HTTPClient: &http.Client{
						Timeout: 2 * time.Millisecond,
					},
				}
			},
			wantErr:    context.DeadlineExceeded,
			thereIsErr: true,
		},
		{
			name: "connection_refused",
			options: func(_ *httptest.Server) crawler.Options {
				return crawler.Options{
					URL:        "http://localhost:65535",
					HTTPClient: &http.Client{},
				}
			},
			wantErr:    syscall.ECONNREFUSED,
			thereIsErr: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			got, err := crawler.Analyze(t.Context(), tc.options(server))
			if tc.thereIsErr {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)

			var response crawler.Response
			jErr := json.Unmarshal(got, &response)
			require.NoError(t, jErr)

			assert.Equal(t, tc.wantResponse.Pages[0].HTTPStatus, response.Pages[0].HTTPStatus)
			assert.Equal(t, tc.wantResponse.Pages[0].Error, response.Pages[0].Error)
		})
	}
}

func TestFindBrokenLinks(t *testing.T) {
	t.Parallel()
	testLinks := []string{"https://www.google.com", "https://github.com",
		"https://www.google.com/non-existent-page", "https://wooordhunt.ru/word/хтрй"}

	opts := crawler.Options{
		HTTPClient: &http.Client{},
		UserAgent:  "curl/8.14.1",
	}
	got, err := crawler.FindBrokenLinks(testLinks, opts)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestCollectSEO(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		name    string
		handler http.Handler
		wantSEO *crawler.Seo
	}{
		{
			name: "seo_positive_test",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.ServeFile(writer, request, "/home/alex/go-project-316/testdata/seo_test/positive_test.html")
			}),
			wantSEO: &crawler.Seo{
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
				http.ServeFile(writer, request, "/home/alex/go-project-316/testdata/seo_test/negative_test.html")
			}),
			wantSEO: &crawler.Seo{
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
			seo, err := crawler.CollectSEO(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.wantSEO, seo)
		})
	}
}
