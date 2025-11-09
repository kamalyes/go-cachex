/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:56:49
 * @FilePath: \go-cachex\twolevel.go
 * @Description: 两级缓存封装（TwoLevelHandler）
 *
 * 提供 L1（快速、本地）与 L2（容量大或远程）两级缓存组合：L1 命中直接返回，
 * L1 未命中时回退到 L2 并将数据提升（promote）到 L1, 写入支持同步写入（write-through）
 * 或异步回填策略
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"time"
)

// TwoLevelHandler 提供 L1/L2 两级缓存封装，兼容 Handler 接口
// L1 通常是快速但容量有限的本地缓存（例如 LRU），L2 是容量更大或远程的缓存（例如 Ristretto/Redis）
type TwoLevelHandler struct {
    L1           Handler
    L2           Handler
    WriteThrough bool // 如果 true，Set 同步写入 L1 和 L2；否则异步写入 L2
}

func NewTwoLevelHandler(l1, l2 Handler, writeThrough bool) *TwoLevelHandler {
    return &TwoLevelHandler{L1: l1, L2: l2, WriteThrough: writeThrough}
}

func (h *TwoLevelHandler) Set(key, value []byte) error {
    if h.WriteThrough {
        if err := h.L1.Set(key, value); err != nil {
            return err
        }
        return h.L2.Set(key, value)
    }
    // async to L2
    if err := h.L1.Set(key, value); err != nil {
        return err
    }
    go func() { _ = h.L2.Set(key, value) }()
    return nil
}

func (h *TwoLevelHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
    if h.WriteThrough {
        if err := h.L1.SetWithTTL(key, value, ttl); err != nil {
            return err
        }
        return h.L2.SetWithTTL(key, value, ttl)
    }
    if err := h.L1.SetWithTTL(key, value, ttl); err != nil {
        return err
    }
    go func() { _ = h.L2.SetWithTTL(key, value, ttl) }()
    return nil
}

func (h *TwoLevelHandler) Get(key []byte) ([]byte, error) {
    if v, err := h.L1.Get(key); err == nil {
        return v, nil
    }
    // L1 miss -> check L2
    v2, err2 := h.L2.Get(key)
    if err2 != nil {
        return nil, ErrNotFound
    }
    // promote to L1; best-effort for TTL
    if ttl, err := h.L2.GetTTL(key); err == nil {
        if ttl > 0 {
            _ = h.L1.SetWithTTL(key, v2, ttl)
        } else {
            _ = h.L1.Set(key, v2)
        }
    } else {
        _ = h.L1.Set(key, v2)
    }
    return v2, nil
}

func (h *TwoLevelHandler) GetTTL(key []byte) (time.Duration, error) {
    if ttl, err := h.L1.GetTTL(key); err == nil {
        return ttl, nil
    }
    return h.L2.GetTTL(key)
}

func (h *TwoLevelHandler) Del(key []byte) error {
    _ = h.L1.Del(key)
    return h.L2.Del(key)
}

// BatchGet 批量获取多个键的值
func (h *TwoLevelHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	if len(keys) == 0 {
		return nil, nil
	}

	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))
	needL2 := make([]int, 0, len(keys))
	l2Keys := make([][]byte, 0, len(keys))

	// 先从L1缓存批量获取
	l1Results, l1Errors := h.L1.BatchGet(keys)
	
	// 找出L1未命中的项
	for i, err := range l1Errors {
		if err == nil {
			results[i] = l1Results[i]
		} else {
			needL2 = append(needL2, i)
			l2Keys = append(l2Keys, keys[i])
		}
	}

	// 如果有L1未命中的项，从L2获取
	if len(l2Keys) > 0 {
		l2Results, l2Errors := h.L2.BatchGet(l2Keys)
		
		for j, idx := range needL2 {
			if l2Errors[j] == nil {
				// L2命中，数据同时提升到L1
				value := l2Results[j]
				results[idx] = value
				errors[idx] = nil
				
				// 异步提升到L1，避免阻塞
				go func(k, v []byte) {
					h.L1.Set(k, v)
				}(keys[idx], value)
			} else {
				errors[idx] = l2Errors[j]
			}
		}
	}

	return results, errors
}

// Stats 返回两级缓存的统计信息
func (h *TwoLevelHandler) Stats() map[string]interface{} {
	l1Stats := h.L1.Stats()
	l2Stats := h.L2.Stats()
	
	return map[string]interface{}{
		"l1_cache": l1Stats,
		"l2_cache": l2Stats,
		"cache_type": "two_level",
	}
}

// GetOrCompute 获取缓存值，如果不存在则计算并设置
func (h *TwoLevelHandler) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	// 先尝试从L1获取
	if value, err := h.L1.Get(key); err == nil {
		return value, nil
	}

	// 再尝试从L2获取
	if value, err := h.L2.Get(key); err == nil {
		// 异步提升到L1
		go func() {
			if ttl <= 0 {
				h.L1.Set(key, value)
			} else {
				h.L1.SetWithTTL(key, value, ttl)
			}
		}()
		return value, nil
	}

	// 两级缓存都未命中，调用loader
	value, err := loader()
	if err != nil {
		return nil, err
	}

	// 将结果写入两级缓存
	if ttl <= 0 {
		h.L1.Set(key, value)
		h.L2.Set(key, value)
	} else {
		h.L1.SetWithTTL(key, value, ttl)
		h.L2.SetWithTTL(key, value, ttl)
	}

	// 返回值的拷贝
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

func (h *TwoLevelHandler) Close() error {
    var lastErr error
    if err := h.L1.Close(); err != nil {
        lastErr = err
    }
    if err := h.L2.Close(); err != nil {
        lastErr = err
    }
    return lastErr
}
