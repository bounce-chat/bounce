package ui

import (
	"container/list"
	"image"
	"sync"
)

// A thumbnailCache holds the scaled-down copies of image attachments that chat bubbles display, bounded by the
// bytes of pixels it holds, dropping the least recently shown first.  Full size decodes are never kept here: the
// image viewer decodes an attachment when it is opened and drops it when it is closed.
type thumbnailCache struct {
	sync.Mutex
	budget  int
	used    int
	order   *list.List // most recently used at the front
	entries map[string]*list.Element
}

type thumbnail struct {
	key   string
	image image.Image
}

func newThumbnailCache(budget int) *thumbnailCache {
	return &thumbnailCache{
		budget:  budget,
		order:   list.New(),
		entries: map[string]*list.Element{},
	}
}

func imageBytes(img image.Image) int {
	bounds := img.Bounds()
	return bounds.Dx() * bounds.Dy() * 4
}

func (c *thumbnailCache) get(key string) (image.Image, bool) {
	c.Lock()
	defer c.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return element.Value.(thumbnail).image, true
}

func (c *thumbnailCache) put(key string, img image.Image) {
	c.Lock()
	defer c.Unlock()
	if element, ok := c.entries[key]; ok {
		c.used -= imageBytes(element.Value.(thumbnail).image)
		c.order.Remove(element)
	}
	c.entries[key] = c.order.PushFront(thumbnail{key: key, image: img})
	c.used += imageBytes(img)
	for c.used > c.budget && c.order.Len() > 1 {
		oldest := c.order.Back()
		c.used -= imageBytes(oldest.Value.(thumbnail).image)
		delete(c.entries, oldest.Value.(thumbnail).key)
		c.order.Remove(oldest)
	}
}

func (c *thumbnailCache) remove(key string) {
	c.Lock()
	defer c.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return
	}
	c.used -= imageBytes(element.Value.(thumbnail).image)
	delete(c.entries, key)
	c.order.Remove(element)
}
