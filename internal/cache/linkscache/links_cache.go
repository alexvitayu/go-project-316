package linkscache

import (
	"net/http"
	"sync"
)

type cacheEntity struct {
	body       []byte
	statusCode int
	status     string
	err        error
	method     string
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

func (c *LinksCache) AddToCache(url string, err error, statusCode int, method, status string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.cache[url]; !exists {
		c.cache[url] = &cacheEntity{
			body:       body,
			statusCode: statusCode,
			status:     status,
			err:        err,
			method:     method,
		}
	}
}

func (c *LinksCache) IsThereInCache(url string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.cache[url]
	return ok
}

func (c *LinksCache) IsBodyNil(url string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if entity, exists := c.cache[url]; exists {
		return entity.body == nil
	}
	return true
}

func (c *LinksCache) TakeFromCache(url string) (*http.Response, []byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entity, exists := c.cache[url]
	if !exists {
		return nil, nil, nil
	}
	if entity.err != nil {
		return nil, nil, entity.err
	}
	resp := &http.Response{
		StatusCode:    entity.statusCode,
		Status:        entity.status,
		ContentLength: int64(len(entity.body)),
	}
	body := c.cache[url].body
	return resp, body, nil
}

func (c *LinksCache) TakeMethodFromCache(url string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var method string
	if entity, exists := c.cache[url]; exists {
		method = entity.method
	}
	return method
}
