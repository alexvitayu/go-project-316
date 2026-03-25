package assetscache

import (
	"code/internal/models"
	"sync"
)

type AssetsCache struct {
	Cache map[string]models.Assets
	Mu    *sync.Mutex
}

func NewCacheAssets() *AssetsCache {
	return &AssetsCache{
		Cache: make(map[string]models.Assets),
		Mu:    &sync.Mutex{},
	}
}

func (c *AssetsCache) AddToCache(url string, assets models.Assets) {
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

func (c *AssetsCache) TakeFromCache(url string) models.Assets {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	asset, exists := c.Cache[url]
	if exists {
		return asset
	}
	return models.Assets{}
}
