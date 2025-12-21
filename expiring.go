/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-06 21:15:15
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:16:12
 * @FilePath: \go-cachex\expiring.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"sync"
	"time"
)

// ExpiringHandler 是一个简单的基于 map 的线程安全内存缓存，支持 TTL 和后台清理
// 它实现了 Handler 接口
type ExpiringHandler struct {
	mu     sync.RWMutex
	items  map[string]expItem
	ticker *time.Ticker
	stop   chan struct{}
	closed bool
	wg     sync.WaitGroup
}

type expItem struct {
	val    []byte
	expiry time.Time // zero = no expiry
}

// NewExpiringHandler 创建一个新的 ExpiringHandler，cleanupInterval 控制后台清理频率（为0则使用1s）
func NewExpiringHandler(cleanupInterval time.Duration) *ExpiringHandler {
	if cleanupInterval <= 0 {
		cleanupInterval = time.Second
	}
	h := &ExpiringHandler{
		items:  make(map[string]expItem),
		ticker: time.NewTicker(cleanupInterval),
		stop:   make(chan struct{}),
	}
	h.wg.Add(1)
	go h.cleanupLoop()
	return h
}

func (h *ExpiringHandler) cleanupLoop() {
	defer h.wg.Done()
	for {
		select {
		case <-h.ticker.C:
			h.mu.Lock()
			now := time.Now()
			for k, it := range h.items {
				if !it.expiry.IsZero() && now.After(it.expiry) {
					delete(h.items, k)
				}
			}
			h.mu.Unlock()
		case <-h.stop:
			// stop requested, exit goroutine
			return
		}
	}
}

func copyB(b []byte) []byte {
	if b == nil {
		return nil
	}
	nb := make([]byte, len(b))
	copy(nb, b)
	return nb
}

// ========== 简化版方法（不带context） ==========

func (h *ExpiringHandler) Get(key []byte) ([]byte, error) {
	return h.GetWithCtx(context.Background(), key)
}

func (h *ExpiringHandler) GetTTL(key []byte) (time.Duration, error) {
	return h.GetTTLWithCtx(context.Background(), key)
}

func (h *ExpiringHandler) Set(key, value []byte) error {
	return h.SetWithCtx(context.Background(), key, value)
}

func (h *ExpiringHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
	return h.SetWithTTLAndCtx(context.Background(), key, value, ttl)
}

func (h *ExpiringHandler) Del(key []byte) error {
	return h.DelWithCtx(context.Background(), key)
}

func (h *ExpiringHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	return h.BatchGetWithCtx(context.Background(), keys)
}

// ========== 完整版方法（带context） ==========

// SetWithCtx 将值写入缓存（无TTL，支持context）
func (h *ExpiringHandler) SetWithCtx(ctx context.Context, key, value []byte) error {
	return h.SetWithTTLAndCtx(ctx, key, value, -1) // -1 表示永不过期
}

// SetWithTTLAndCtx 写入值并设置TTL（ttl<=0 表示不过期，支持context）
func (h *ExpiringHandler) SetWithTTLAndCtx(ctx context.Context, key, value []byte, ttl time.Duration) error {
	if err := ValidateWriteWithTTLOp(key, value, ttl, true, h.closed); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	sk := string(key)
	it := expItem{val: copyB(value)}
	if ttl > 0 {
		it.expiry = time.Now().Add(ttl)
	} else if ttl == 0 {
		// 0 表示立即过期
		it.expiry = time.Now().Add(-time.Second)
	} else if ttl == -1 {
		// -1 表示永不过期，保持 expiry 为零值
		it.expiry = time.Time{}
	}
	h.items[sk] = it
	return nil
}

// GetWithCtx 读取值，如果不存在或已过期返回ErrNotFound
func (h *ExpiringHandler) GetWithCtx(ctx context.Context, key []byte) ([]byte, error) {
	if err := ValidateBasicOp(key, true, h.closed); err != nil {
		return nil, err
	}

	h.mu.RLock()
	it, ok := h.items[string(key)]
	h.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if !it.expiry.IsZero() && time.Now().After(it.expiry) {
		// lazy delete
		h.mu.Lock()
		// re-check and delete
		if cur, ok2 := h.items[string(key)]; ok2 {
			if !cur.expiry.IsZero() && time.Now().After(cur.expiry) {
				delete(h.items, string(key))
			}
		}
		h.mu.Unlock()
		return nil, ErrNotFound
	}
	return copyB(it.val), nil
}

// GetTTLWithCtx 返回剩余TTL（0 表示无TTL），找不到返回ErrNotFound
func (h *ExpiringHandler) GetTTLWithCtx(ctx context.Context, key []byte) (time.Duration, error) {
	if err := ValidateBasicOp(key, true, h.closed); err != nil {
		return 0, err
	}

	h.mu.RLock()
	it, ok := h.items[string(key)]
	h.mu.RUnlock()
	if !ok {
		return 0, ErrNotFound
	}
	if it.expiry.IsZero() {
		return 0, nil
	}
	return time.Until(it.expiry), nil
}

// DelWithCtx 删除键
func (h *ExpiringHandler) DelWithCtx(ctx context.Context, key []byte) error {
	if err := ValidateBasicOp(key, true, h.closed); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.items, string(key))
	return nil
}

// BatchGetWithCtx 批量获取多个键的值
func (h *ExpiringHandler) BatchGetWithCtx(ctx context.Context, keys [][]byte) ([][]byte, []error) {
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

	now := time.Now()
	for i, key := range keys {
		if len(key) == 0 {
			errors[i] = ErrInvalidKey
			continue
		}

		sk := string(key)
		if item, found := h.items[sk]; found {
			if !item.expiry.IsZero() && now.After(item.expiry) {
				// 已过期但还在map中，视为未找到
				errors[i] = ErrNotFound
			} else {
				// 复制数据避免外部修改
				valueCopy := make([]byte, len(item.val))
				copy(valueCopy, item.val)
				results[i] = valueCopy
			}
		} else {
			errors[i] = ErrNotFound
		}
	}

	return results, errors
}

// GetOrCompute 获取缓存值，如果不存在则计算并设置
func (h *ExpiringHandler) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	ctxLoader := func(context.Context) ([]byte, error) { return loader() }
	return h.GetOrComputeWithCtx(context.Background(), key, ttl, ctxLoader)
}

// GetOrComputeWithCtx 获取缓存值，如果不存在则计算并设置
func (h *ExpiringHandler) GetOrComputeWithCtx(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}

	sk := string(key)

	// 首先尝试获取
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return nil, ErrClosed
	}

	if item, found := h.items[sk]; found {
		now := time.Now()
		if !item.expiry.IsZero() && now.After(item.expiry) {
			// 过期，需要重新计算
			h.mu.RUnlock()
		} else {
			// 复制数据避免外部修改
			result := make([]byte, len(item.val))
			copy(result, item.val)
			h.mu.RUnlock()
			return result, nil
		}
	} else {
		h.mu.RUnlock()
	}

	// 检查context是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// 缓存未命中或已过期，调用loader
	value, err := loader(ctx)
	if err != nil {
		return nil, err
	}

	// 将结果写入缓存
	if ttl <= 0 {
		h.SetWithCtx(ctx, key, value)
	} else {
		h.SetWithTTLAndCtx(ctx, key, value, ttl)
	}

	// 返回值的拷贝
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

// Stats 返回缓存统计信息
func (h *ExpiringHandler) Stats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return map[string]interface{}{
			"closed":        true,
			"entries":       0,
			"expired_items": 0,
		}
	}

	// 计算过期项
	expiredCount := 0
	now := time.Now()
	for _, item := range h.items {
		if !item.expiry.IsZero() && now.After(item.expiry) {
			expiredCount++
		}
	}

	return map[string]interface{}{
		"entries":       len(h.items),
		"expired_items": expiredCount,
		"closed":        h.closed,
		"cache_type":    "expiring",
	}
}

// Close 停止后台清理并释放资源
func (h *ExpiringHandler) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	// signal goroutine to stop and stop the ticker
	close(h.stop)
	h.ticker.Stop()
	h.mu.Unlock()
	h.wg.Wait()
	return nil
}
