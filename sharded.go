/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:35:15
 * @FilePath: \go-cachex\sharded.go
 * @Description: 分片缓存封装（ShardedHandler）
 *
 * 将缓存分为多个 shard，每个 shard 使用独立的 Handler 实例（例如多个 LRU）
 * 适用于降低单个实例的锁竞争或将负载分散到多个后端实例的场景
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"hash/fnv"
	"time"
)

// ShardedHandler 将缓存分成多个 shard，每个 shard 使用独立的 Handler 实例
// 适用于降低单锁争用或使用多实例后端的场景
type ShardedHandler struct {
    shards []Handler
    n      int
}

// NewShardedHandler 使用 factory 创建 shards 个 Handler
func NewShardedHandler(factory func() Handler, shards int) *ShardedHandler {
    if shards <= 0 {
        shards = 16
    }
    s := make([]Handler, shards)
    for i := 0; i < shards; i++ {
        s[i] = factory()
    }
    return &ShardedHandler{shards: s, n: shards}
}

func (h *ShardedHandler) shardFor(key []byte) Handler {
    hasher := fnv.New32a()
    hasher.Write(key)
    idx := hasher.Sum32() % uint32(h.n)
    return h.shards[int(idx)]
}

// Set 将请求转发到对应的 shard
func (h *ShardedHandler) Set(key, value []byte) error {
    return h.shardFor(key).Set(key, value)
}

// SetWithTTL 转发
func (h *ShardedHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
    return h.shardFor(key).SetWithTTL(key, value, ttl)
}

// Get 转发
func (h *ShardedHandler) Get(key []byte) ([]byte, error) {
    return h.shardFor(key).Get(key)
}

// GetTTL 转发
func (h *ShardedHandler) GetTTL(key []byte) (time.Duration, error) {
    return h.shardFor(key).GetTTL(key)
}

// Del 转发
func (h *ShardedHandler) Del(key []byte) error {
    return h.shardFor(key).Del(key)
}

// Close 关闭所有 shard
func (h *ShardedHandler) Close() error {
    var lastErr error
    for _, s := range h.shards {
        if err := s.Close(); err != nil {
            lastErr = err
        }
    }
    return lastErr
}
