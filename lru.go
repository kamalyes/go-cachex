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
    return h.SetWithTTL(key, value, -1) // -1 表示永不过期
}

// SetWithTTL 实现 Handler.SetWithTTL
func (h *LRUHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
    if key == nil {
        return ErrInvalidKey
    }
    if value == nil {
        return ErrInvalidValue
    }
    if ttl < -1 {
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
        } else if ttl == 0 {
            // 0 表示立即过期
            ent.expiry = time.Now().Add(-time.Second) 
        } else if ttl == -1 {
            // -1 表示永不过期，保持 expiry 为零值  
            ent.expiry = time.Time{}
        } else {
            ent.expiry = time.Time{}
        }
        h.ll.MoveToFront(ele)
        return nil
    }

    ent := &lruEntry{key: sk, value: copyBytes(value)}
    if ttl > 0 {
        ent.expiry = time.Now().Add(ttl)
    } else if ttl == 0 {
        // 0 表示立即过期
        ent.expiry = time.Now().Add(-time.Second)
    } else if ttl == -1 {
        // -1 表示永不过期，保持 expiry 为零值
        ent.expiry = time.Time{}
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

// BatchGet 批量获取多个键的值
func (h *LRUHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	if len(keys) == 0 {
		return nil, nil
	}

	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		for i := range errors {
			errors[i] = ErrClosed
		}
		return results, errors
	}

	for i, key := range keys {
		if len(key) == 0 {
			errors[i] = ErrInvalidKey
			continue
		}

		sk := string(key)
		if ele, hit := h.cache[sk]; hit {
			entry := ele.Value.(*lruEntry)
			// 检查TTL
			if !entry.expiry.IsZero() && time.Now().After(entry.expiry) {
				// 过期，删除并返回未找到
				h.ll.Remove(ele)
				delete(h.cache, sk)
				errors[i] = ErrNotFound
			} else {
				// 移动到前面（最近访问）
				h.ll.MoveToFront(ele)
				// 复制数据避免外部修改
				valueCopy := make([]byte, len(entry.value))
				copy(valueCopy, entry.value)
				results[i] = valueCopy
			}
		} else {
			errors[i] = ErrNotFound
		}
	}

	return results, errors
}

// Stats 返回缓存统计信息
func (h *LRUHandler) Stats() map[string]interface{} {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return map[string]interface{}{
			"closed":   true,
			"entries":  0,
			"capacity": h.maxEntries,
		}
	}

	// 计算过期项
	expiredCount := 0
	now := time.Now()
	for _, ele := range h.cache {
		entry := ele.Value.(*lruEntry)
		if !entry.expiry.IsZero() && now.After(entry.expiry) {
			expiredCount++
		}
	}

	return map[string]interface{}{
		"entries":       h.ll.Len(),
		"capacity":      h.maxEntries,
		"expired_items": expiredCount,
		"closed":        h.closed,
	}
}

// GetOrCompute 获取缓存值，如果不存在则计算并设置
func (h *LRUHandler) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}

	sk := string(key)

	// 首先尝试获取
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrClosed
	}

	if ele, hit := h.cache[sk]; hit {
		entry := ele.Value.(*lruEntry)
		// 检查TTL
		if !entry.expiry.IsZero() && time.Now().After(entry.expiry) {
			// 过期，删除
			h.ll.Remove(ele)
			delete(h.cache, sk)
		} else {
			// 移动到前面并返回
			h.ll.MoveToFront(ele)
			valueCopy := copyBytes(entry.value)
			h.mu.Unlock()
			return valueCopy, nil
		}
	}
	h.mu.Unlock()

	// 缓存未命中，调用loader
	value, err := loader()
	if err != nil {
		return nil, err
	}

	// 将结果写入缓存
	if ttl <= 0 {
		h.Set(key, value)
	} else {
		h.SetWithTTL(key, value, ttl)
	}

	return copyBytes(value), nil
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
