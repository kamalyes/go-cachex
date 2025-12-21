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
	"context"
	"fmt"
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

// ========== 简化版方法（不带context） ==========

// Get 获取缓存值
func (h *ShardedHandler) Get(key []byte) ([]byte, error) {
	return h.GetWithCtx(context.Background(), key)
}

// GetTTL 获取键的剩余TTL
func (h *ShardedHandler) GetTTL(key []byte) (time.Duration, error) {
	return h.GetTTLWithCtx(context.Background(), key)
}

// Set 设置缓存
func (h *ShardedHandler) Set(key, value []byte) error {
	return h.SetWithCtx(context.Background(), key, value)
}

// SetWithTTL 设置带TTL的缓存
func (h *ShardedHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
	return h.SetWithTTLAndCtx(context.Background(), key, value, ttl)
}

// Del 删除键
func (h *ShardedHandler) Del(key []byte) error {
	return h.DelWithCtx(context.Background(), key)
}

// BatchGet 批量获取
func (h *ShardedHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	return h.BatchGetWithCtx(context.Background(), keys)
}

// ========== 完整版方法（带context） ==========

// SetWithCtx 将请求转发到对应的shard
func (h *ShardedHandler) SetWithCtx(ctx context.Context, key, value []byte) error {
	return h.shardFor(key).SetWithCtx(ctx, key, value)
}

// SetWithTTLAndCtx 转发
func (h *ShardedHandler) SetWithTTLAndCtx(ctx context.Context, key, value []byte, ttl time.Duration) error {
	return h.shardFor(key).SetWithTTLAndCtx(ctx, key, value, ttl)
}

// GetWithCtx 转发
func (h *ShardedHandler) GetWithCtx(ctx context.Context, key []byte) ([]byte, error) {
	return h.shardFor(key).GetWithCtx(ctx, key)
}

// GetTTLWithCtx 转发
func (h *ShardedHandler) GetTTLWithCtx(ctx context.Context, key []byte) (time.Duration, error) {
	return h.shardFor(key).GetTTLWithCtx(ctx, key)
}

// DelWithCtx 转发
func (h *ShardedHandler) DelWithCtx(ctx context.Context, key []byte) error {
	return h.shardFor(key).DelWithCtx(ctx, key)
}

// BatchGetWithCtx 批量获取多个键的值
func (h *ShardedHandler) BatchGetWithCtx(ctx context.Context, keys [][]byte) ([][]byte, []error) {
	if len(keys) == 0 {
		return nil, nil
	}

	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))

	// 按分片分组键
	shardGroups := make(map[int][]int)
	for i, key := range keys {
		hasher := fnv.New32a()
		hasher.Write(key)
		shardIdx := int(hasher.Sum32() % uint32(h.n))
		shardGroups[shardIdx] = append(shardGroups[shardIdx], i)
	}

	// 并发处理各个分片
	type shardResult struct {
		shardIdx int
		results  [][]byte
		errors   []error
		indices  []int
	}

	resultChan := make(chan shardResult, len(shardGroups))

	for shardIdx, indices := range shardGroups {
		go func(sIdx int, idxs []int) {
			shardKeys := make([][]byte, len(idxs))
			for i, idx := range idxs {
				shardKeys[i] = keys[idx]
			}

			shardResults, shardErrors := h.shards[sIdx].BatchGetWithCtx(ctx, shardKeys)
			resultChan <- shardResult{
				shardIdx: sIdx,
				results:  shardResults,
				errors:   shardErrors,
				indices:  idxs,
			}
		}(shardIdx, indices)
	}

	// 收集结果
	for range shardGroups {
		result := <-resultChan
		for i, idx := range result.indices {
			results[idx] = result.results[i]
			errors[idx] = result.errors[i]
		}
	}

	return results, errors
}

// Stats 返回所有分片的统计信息
func (h *ShardedHandler) Stats() map[string]interface{} {
	allStats := make(map[string]interface{})

	for i, shard := range h.shards {
		shardStats := shard.Stats()
		allStats[fmt.Sprintf("shard_%d", i)] = shardStats
	}

	allStats["total_shards"] = len(h.shards)
	allStats["cache_type"] = "sharded"

	return allStats
}

// GetOrCompute 获取缓存值，如果不存在则计算并设置
func (h *ShardedHandler) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	ctxLoader := func(context.Context) ([]byte, error) { return loader() }
	return h.GetOrComputeWithCtx(context.Background(), key, ttl, ctxLoader)
}

// GetOrComputeWithCtx 获取或计算值
func (h *ShardedHandler) GetOrComputeWithCtx(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	return h.shardFor(key).GetOrComputeWithCtx(ctx, key, ttl, loader)
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
