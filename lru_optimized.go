/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 22:30:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 22:30:00
 * @FilePath: \go-cachex\lru_optimized.go
 * @Description: 高性能优化版本的 LRU 缓存实现
 *
 * 主要优化点:
 * 1. 读写锁分离提升并发读性能
 * 2. 减少内存分配和复制
 * 3. 对象池减少GC压力
 * 4. 批量操作支持
 * 5. 内存预分配优化
 * 
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"container/list"
	"sync"
	"time"
	"unsafe"
)

// LRUOptimizedHandler 是高性能优化版本的 LRU 缓存
type LRUOptimizedHandler struct {
	mu         sync.RWMutex
	maxEntries int
	ll         *list.List
	cache      map[string]*list.Element
	closed     bool
	
	// 对象池减少GC压力
	entryPool sync.Pool
}

// 优化的entry结构，减少内存占用
type lruOptEntry struct {
	key    string
	value  []byte
	expiry int64 // 使用纳秒时间戳，避免time.Time的开销
}

// NewLRUOptimizedHandler 创建优化版本的LRU缓存
func NewLRUOptimizedHandler(maxEntries int) *LRUOptimizedHandler {
	initialCap := maxEntries
	if initialCap <= 0 {
		initialCap = 16 // 默认初始容量
	}
	
	h := &LRUOptimizedHandler{
		maxEntries: maxEntries,
		ll:         list.New(),
		cache:      make(map[string]*list.Element, initialCap),
	}
	
	// 初始化对象池
	h.entryPool = sync.Pool{
		New: func() interface{} {
			return &lruOptEntry{}
		},
	}
	
	return h
}

// 快速字节转字符串，避免内存分配
func unsafeBytesToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// 获取当前时间的纳秒时间戳
func nowNano() int64 {
	return time.Now().UnixNano()
}

// 检查是否过期
func (h *LRUOptimizedHandler) isExpired(expiry int64) bool {
	return expiry > 0 && nowNano() > expiry
}

// purgeExpired 检查并移除过期元素（需要在写锁下调用）
func (h *LRUOptimizedHandler) purgeExpired(e *list.Element) bool {
	if e == nil {
		return false
	}
	ent := e.Value.(*lruOptEntry)
	if h.isExpired(ent.expiry) {
		h.ll.Remove(e)
		delete(h.cache, ent.key)
		// 回收到对象池
		h.recycleEntry(ent)
		return true
	}
	return false
}

// 从对象池获取entry
func (h *LRUOptimizedHandler) getEntry() *lruOptEntry {
	return h.entryPool.Get().(*lruOptEntry)
}

// 回收entry到对象池
func (h *LRUOptimizedHandler) recycleEntry(ent *lruOptEntry) {
	// 清理数据
	ent.key = ""
	ent.value = nil
	ent.expiry = 0
	h.entryPool.Put(ent)
}

// Set 实现 Handler.Set
func (h *LRUOptimizedHandler) Set(key, value []byte) error {
	return h.SetWithTTL(key, value, -1)
}

// SetWithTTL 实现 Handler.SetWithTTL
func (h *LRUOptimizedHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
	if key == nil {
		return ErrInvalidKey
	}
	if value == nil {
		return ErrInvalidValue
	}
	if ttl < -1 {
		return ErrInvalidTTL
	}
	
	sk := unsafeBytesToString(key)
	var expiry int64
	
	// 计算过期时间
	if ttl > 0 {
		expiry = nowNano() + ttl.Nanoseconds()
	} else if ttl == 0 {
		expiry = nowNano() - 1 // 立即过期
	} // ttl == -1 时 expiry 保持为 0（永不过期）
	
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if h.closed {
		return ErrClosed
	}
	
	if ele, ok := h.cache[sk]; ok {
		// 更新现有条目
		ent := ele.Value.(*lruOptEntry)
		// 直接覆盖value，减少内存分配
		if cap(ent.value) >= len(value) {
			ent.value = ent.value[:len(value)]
			copy(ent.value, value)
		} else {
			ent.value = make([]byte, len(value))
			copy(ent.value, value)
		}
		ent.expiry = expiry
		h.ll.MoveToFront(ele)
		return nil
	}
	
	// 创建新条目
	ent := h.getEntry()
	ent.key = string(key) // 这里需要真正分配字符串
	ent.value = make([]byte, len(value))
	copy(ent.value, value)
	ent.expiry = expiry
	
	ele := h.ll.PushFront(ent)
	h.cache[sk] = ele
	
	// 检查容量限制
	if h.maxEntries > 0 && h.ll.Len() > h.maxEntries {
		back := h.ll.Back()
		if back != nil {
			old := back.Value.(*lruOptEntry)
			delete(h.cache, old.key)
			h.ll.Remove(back)
			h.recycleEntry(old)
		}
	}
	
	return nil
}

// Get 实现 Handler.Get - 使用读锁提升并发性能
func (h *LRUOptimizedHandler) Get(key []byte) ([]byte, error) {
	if key == nil {
		return nil, ErrInvalidKey
	}
	
	sk := unsafeBytesToString(key)
	
	// 先用读锁查找
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return nil, ErrClosed
	}
	
	ele, ok := h.cache[sk]
	if !ok {
		h.mu.RUnlock()
		return nil, ErrNotFound
	}
	
	ent := ele.Value.(*lruOptEntry)
	
	// 快速检查是否过期
	if h.isExpired(ent.expiry) {
		h.mu.RUnlock()
		// 升级到写锁进行清理
		h.mu.Lock()
		if h.purgeExpired(ele) {
			h.mu.Unlock()
			return nil, ErrNotFound
		}
		h.mu.Unlock()
		return nil, ErrNotFound
	}
	
	// 复制数据
	result := make([]byte, len(ent.value))
	copy(result, ent.value)
	h.mu.RUnlock()
	
	// 升级到写锁移动到前面（LRU更新）
	h.mu.Lock()
	if ele.Value != nil { // 确保元素仍然存在
		h.ll.MoveToFront(ele)
	}
	h.mu.Unlock()
	
	return result, nil
}

// GetTTL 实现 Handler.GetTTL
func (h *LRUOptimizedHandler) GetTTL(key []byte) (time.Duration, error) {
	if key == nil {
		return 0, ErrInvalidKey
	}
	
	sk := unsafeBytesToString(key)
	
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	if h.closed {
		return 0, ErrClosed
	}
	
	ele, ok := h.cache[sk]
	if !ok {
		return 0, ErrNotFound
	}
	
	ent := ele.Value.(*lruOptEntry)
	if h.isExpired(ent.expiry) {
		return 0, ErrNotFound
	}
	
	if ent.expiry == 0 {
		return 0, nil // 永不过期
	}
	
	remaining := ent.expiry - nowNano()
	if remaining <= 0 {
		return 0, ErrNotFound
	}
	
	return time.Duration(remaining), nil
}

// Del 实现 Handler.Del
func (h *LRUOptimizedHandler) Del(key []byte) error {
	if key == nil {
		return ErrInvalidKey
	}
	
	sk := unsafeBytesToString(key)
	
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if h.closed {
		return ErrClosed
	}
	
	if ele, ok := h.cache[sk]; ok {
		ent := ele.Value.(*lruOptEntry)
		h.ll.Remove(ele)
		delete(h.cache, sk)
		h.recycleEntry(ent)
	}
	
	return nil
}

// Close 实现 Handler.Close
func (h *LRUOptimizedHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if h.closed {
		return nil
	}
	
	h.closed = true
	
	// 清理并回收所有条目
	for h.ll.Len() > 0 {
		back := h.ll.Back()
		if back != nil {
			ent := back.Value.(*lruOptEntry)
			h.ll.Remove(back)
			h.recycleEntry(ent)
		}
	}
	
	h.ll = nil
	h.cache = make(map[string]*list.Element)
	return nil
}

// BatchGet 批量获取，减少锁开销
func (h *LRUOptimizedHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	if len(keys) == 0 {
		return nil, nil
	}
	
	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))
	
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	if h.closed {
		for i := range errors {
			errors[i] = ErrClosed
		}
		return results, errors
	}
	
	for i, key := range keys {
		if key == nil {
			errors[i] = ErrInvalidKey
			continue
		}
		
		sk := unsafeBytesToString(key)
		ele, ok := h.cache[sk]
		if !ok {
			errors[i] = ErrNotFound
			continue
		}
		
		ent := ele.Value.(*lruOptEntry)
		if h.isExpired(ent.expiry) {
			errors[i] = ErrNotFound
			continue
		}
		
		results[i] = make([]byte, len(ent.value))
		copy(results[i], ent.value)
	}
	
	return results, errors
}

// Stats 返回缓存统计信息
func (h *LRUOptimizedHandler) Stats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	return map[string]interface{}{
		"entries":    h.ll.Len(),
		"max_entries": h.maxEntries,
		"closed":     h.closed,
	}
}