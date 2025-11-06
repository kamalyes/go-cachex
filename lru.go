/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 22:05:59
 * @FilePath: \go-cachex\lru.go
 * @Description: 简单的 goroutine 安全 LRU 缓存（LRUHandler）
 *
 * 提供可选的每项 TTL 支持，键/值以 []byte 表示（内部以 string 存储键）
 * 该实现适合用作本地内存缓存或测试用例中的轻量缓存
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"container/list"
	"sync"
	"time"
)

// LRUHandler 是一个简单的线程安全 LRU 缓存实现，满足 Handler 接口
// - keys/values 使用 []byte 表示，key 在内部以 string 存储
// - 当超过容量时，按最近最少使用 (LRU) 驱逐
// - 支持可选的 TTL（通过 SetWithTTL）
type LRUHandler struct {
    mu        sync.Mutex
    maxEntries int
    ll        *list.List
    cache     map[string]*list.Element
    closed    bool
}

type lruEntry struct {
    key    string
    value  []byte
    expiry time.Time // zero 表示不过期
}

// NewLRUHandler 创建一个新的 LRUHandler，maxEntries<=0 表示无限容量
func NewLRUHandler(maxEntries int) *LRUHandler {
    return &LRUHandler{
        maxEntries: maxEntries,
        ll:         list.New(),
        cache:      make(map[string]*list.Element),
    }
}

func copyBytes(b []byte) []byte {
    if b == nil {
        return nil
    }
    nb := make([]byte, len(b))
    copy(nb, b)
    return nb
}

// purgeExpired 检查并移除指定元素如果已过期，返回是否被移除
func (h *LRUHandler) purgeExpired(e *list.Element) bool {
    if e == nil {
        return false
    }
    ent := e.Value.(*lruEntry)
    if !ent.expiry.IsZero() && time.Now().After(ent.expiry) {
        // remove
        h.ll.Remove(e)
        delete(h.cache, ent.key)
        return true
    }
    return false
}

// Set 实现 Handler.Set（不设置 TTL）
func (h *LRUHandler) Set(key, value []byte) error {
    return h.SetWithTTL(key, value, 0)
}

// SetWithTTL 实现 Handler.SetWithTTL
func (h *LRUHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
    if key == nil {
        return ErrInvalidKey
    }
    if value == nil {
        return ErrInvalidValue
    }
    if ttl < 0 {
        return ErrInvalidTTL
    }
    
    h.mu.Lock()
    defer h.mu.Unlock()
    if h.closed {
        return ErrClosed
    }
    sk := string(key)
    if ele, ok := h.cache[sk]; ok {
        // 覆盖并移动到前
        ent := ele.Value.(*lruEntry)
        ent.value = copyBytes(value)
        if ttl > 0 {
            ent.expiry = time.Now().Add(ttl)
        } else {
            ent.expiry = time.Time{}
        }
        h.ll.MoveToFront(ele)
        return nil
    }

    ent := &lruEntry{key: sk, value: copyBytes(value)}
    if ttl > 0 {
        ent.expiry = time.Now().Add(ttl)
    }
    ele := h.ll.PushFront(ent)
    h.cache[sk] = ele

    if h.maxEntries > 0 && h.ll.Len() > h.maxEntries {
        // remove oldest
        back := h.ll.Back()
        if back != nil {
            old := back.Value.(*lruEntry)
            delete(h.cache, old.key)
            h.ll.Remove(back)
        }
    }

    return nil
}

// Get 实现 Handler.Get
func (h *LRUHandler) Get(key []byte) ([]byte, error) {
    if err := ValidateBasicOp(key, true, h.closed); err != nil {
        return nil, err
    }

    h.mu.Lock()
    defer h.mu.Unlock()
    sk := string(key)
    ele, ok := h.cache[sk]
    if !ok {
        return nil, ErrNotFound
    }
    if h.purgeExpired(ele) {
        return nil, ErrNotFound
    }
    ent := ele.Value.(*lruEntry)
    h.ll.MoveToFront(ele)
    return copyBytes(ent.value), nil
}

// GetTTL 实现 Handler.GetTTL
func (h *LRUHandler) GetTTL(key []byte) (time.Duration, error) {
    if key == nil {
        return 0, ErrInvalidKey
    }

    h.mu.Lock()
    defer h.mu.Unlock()
    if h.closed {
        return 0, ErrClosed
    }
    sk := string(key)
    ele, ok := h.cache[sk]
    if !ok {
        return 0, ErrNotFound
    }
    if h.purgeExpired(ele) {
        return 0, ErrNotFound
    }
    ent := ele.Value.(*lruEntry)
    if ent.expiry.IsZero() {
        return 0, nil
    }
    return time.Until(ent.expiry), nil
}

// Del 实现 Handler.Del
func (h *LRUHandler) Del(key []byte) error {
    if key == nil {
        return ErrInvalidKey
    }

    h.mu.Lock()
    defer h.mu.Unlock()
    if h.closed {
        return ErrClosed
    }
    sk := string(key)
    if ele, ok := h.cache[sk]; ok {
        h.ll.Remove(ele)
        delete(h.cache, sk)
    }
    return nil
}

// Close 实现 Handler.Close
func (h *LRUHandler) Close() error {
    h.mu.Lock()
    defer h.mu.Unlock()
    if h.closed {
        return nil
    }
    h.closed = true
    h.ll = nil
    h.cache = make(map[string]*list.Element)
    return nil
}
