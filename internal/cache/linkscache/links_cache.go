package linkscache

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

type cacheEntity struct {
	body       []byte
	statusCode int
	status     string
	err        error
}

type LinksCache struct {
	cache map[string]*cacheEntity
	mu    *sync.RWMutex
}

func NewLinksCache() *LinksCache {
	return &LinksCache{
		cache: make(map[string]*cacheEntity),
		mu:    &sync.RWMutex{},
	}
}

func (c *LinksCache) AddToCache(url string, err error, resp *http.Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.cache[url]; !exists {
		entity := &cacheEntity{
			err: err,
		}
		if resp != nil {
			body, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				entity.body = body
				entity.statusCode = resp.StatusCode
				entity.status = resp.Status
			}
		}
		c.cache[url] = entity
	}
}

func (c *LinksCache) IsThereInCache(url string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.cache[url]
	return ok
}

func (c *LinksCache) TakeFromCache(url string) (*http.Response, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entity, exists := c.cache[url]
	if !exists {
		return nil, nil
	}
	if entity.err != nil {
		return nil, entity.err
	}
	resp := &http.Response{
		StatusCode:    entity.statusCode,
		Status:        entity.status,
		Body:          io.NopCloser(bytes.NewReader(entity.body)),
		ContentLength: int64(len(entity.body)),
	}
	return resp, nil
}
