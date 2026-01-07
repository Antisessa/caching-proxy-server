package cache

import (
	"container/list"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type CacheEntry struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	element    *list.Element
}

type Cache struct {
	maps       map[string]*CacheEntry
	linkedList *list.List
	length     int
	mux        sync.Mutex
}

func Init(len int) *Cache {
	return &Cache{
		maps:       make(map[string]*CacheEntry, len),
		linkedList: list.New(),
		length:     len,
		mux:        sync.Mutex{},
	}
}

func (c *Cache) Get(hash string) (CacheEntry, bool) {
	c.mux.Lock()
	defer c.mux.Unlock()

	if entry, ok := c.maps[hash]; ok {
		c.linkedList.MoveToFront(entry.element)
		return *entry, true
	}
	return CacheEntry{}, false
}

// Set new element to Cache
func (c *Cache) Set(key string, resp *http.Response) (CacheEntry, error) {
	c.mux.Lock()
	defer c.mux.Unlock()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return CacheEntry{}, errors.Join(fmt.Errorf("cache set method, reading response body"), err)
	}

	resp.Body.Close() // Не обрабатываем ошибки при закрытии NopCloser

	// Eviction если полный
	if len(c.maps) >= c.length {
		if last := c.linkedList.Back(); last != nil {
			keyToDelete := last.Value.(string)
			c.linkedList.Remove(last)
			delete(c.maps, keyToDelete)
		}
	}

	newEntry := &CacheEntry{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       bodyBytes,
		element:    c.linkedList.PushFront(key),
	}
	c.maps[key] = newEntry
	return *newEntry, nil
}
