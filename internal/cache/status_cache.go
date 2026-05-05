package cache

import (
	"bytes"
	"encoding/json"
	"sync"
	"time"

	"ciallo/internal/mcproto"
)

type StatusCache struct {
	mu      sync.RWMutex
	now     func() time.Time
	entries map[string]entry
}

type entry struct {
	data       []byte
	motd       json.RawMessage
	freshUntil time.Time
	staleUntil time.Time
}

func NewStatusCache(now func() time.Time) *StatusCache {
	if now == nil {
		now = time.Now
	}
	return &StatusCache{
		now:     now,
		entries: make(map[string]entry),
	}
}

func (c *StatusCache) Get(key string) ([]byte, bool) {
	return c.GetFresh(key)
}

func (c *StatusCache) GetFresh(key string) ([]byte, bool) {
	c.mu.RLock()
	item, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || !c.now().Before(item.freshUntil) {
		if ok {
			c.mu.Lock()
			if c.now().After(item.staleUntil) {
				delete(c.entries, key)
			}
			c.mu.Unlock()
		}
		return nil, false
	}
	return append([]byte(nil), item.data...), true
}

func (c *StatusCache) Set(key string, data []byte, ttl time.Duration) {
	c.SetWithFallback(key, data, ttl, 0)
}

func (c *StatusCache) SetWithFallback(key string, data []byte, ttl, fallbackTTL time.Duration) {
	if ttl <= 0 {
		return
	}
	motd := extractMOTD(data)
	now := c.now()
	staleUntil := now.Add(ttl)
	if fallbackTTL > 0 {
		staleUntil = staleUntil.Add(fallbackTTL)
	}
	c.mu.Lock()
	c.entries[key] = entry{
		data:       append([]byte(nil), data...),
		motd:       motd,
		freshUntil: now.Add(ttl),
		staleUntil: staleUntil,
	}
	c.mu.Unlock()
}

func (c *StatusCache) GetFallback(key string, protocolVersion int32) ([]byte, bool) {
	return c.GetFallbackWithOptions(key, protocolVersion, mcproto.FallbackStatusOptions{})
}

func (c *StatusCache) GetFallbackWithOptions(key string, protocolVersion int32, options mcproto.FallbackStatusOptions) ([]byte, bool) {
	c.mu.RLock()
	item, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || c.now().After(item.staleUntil) || len(item.motd) == 0 {
		if ok {
			c.mu.Lock()
			delete(c.entries, key)
			c.mu.Unlock()
		}
		return nil, false
	}
	p, err := mcproto.BuildFallbackStatusWithOptions(protocolVersion, item.motd, options)
	if err != nil {
		return nil, false
	}
	return append([]byte(nil), p.Raw...), true
}

func extractMOTD(data []byte) json.RawMessage {
	packet, err := mcproto.ReadPacket(bytes.NewReader(data), mcproto.MaxPacketLength)
	if err != nil {
		return nil
	}
	statusJSON, err := mcproto.ParseStatusJSON(packet)
	if err != nil {
		return nil
	}
	motd, ok := mcproto.ExtractMOTD(statusJSON)
	if !ok {
		return nil
	}
	return motd
}
