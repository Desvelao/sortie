package preview

import (
	"fmt"
	"sync"
	"time"
)

// Cache is a small in-memory LRU of recent previews (so stepping back and
// forth is instant). It holds decoded, downscaled images only.
type Cache struct {
	mu    sync.Mutex
	max   int
	items map[string]Preview
	order []string // oldest first
}

func NewCache(max int) *Cache { return &Cache{max: max, items: map[string]Preview{}} }

// Key identifies one version of a file.
func Key(path string, size int64, mod time.Time) string {
	return fmt.Sprintf("%s|%d|%d", path, size, mod.UnixNano())
}

func (c *Cache) Get(key string) (Preview, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.items[key]
	if ok {
		c.touch(key)
	}
	return p, ok
}

func (c *Cache) Put(key string, p Preview) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok && len(c.items) >= c.max && len(c.order) > 0 {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
	c.items[key] = p
	c.touch(key)
}

func (c *Cache) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, key)
}
