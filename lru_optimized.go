/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 22:30:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 22:30:00
 * @FilePath: \go-cachex\lru_optimized.go
 * @Description: 超高性能优化版本的 LRU 缓存实现
 *
 * 主要优化点:
 * 1. 分片设计减少锁竞争
 * 2. 原子操作优化热路径
 * 3. 零拷贝字节操作
 * 4. 高效的内存池管理
 * 5. 批量操作支持
 * 6. NUMA友好的内存布局
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

const (
	// 默认分片数量，通常设为CPU核心数的2倍
	defaultShardCount = 16
	// 每个分片的默认容量
	defaultShardCapacity = 64
	// 时间戳缓存更新间隔(毫秒)
	timestampCacheInterval = 10
)

// 全局时间戳缓存，减少系统调用
var (
	cachedTimestamp int64
	lastUpdate      int64
)

// 初始化时间戳缓存
func init() {
	updateCachedTimestamp()
	syncx.Go().OnPanic(nil).Exec(func() {
		ticker := time.NewTicker(time.Millisecond * timestampCacheInterval)
		defer ticker.Stop()
		for range ticker.C {
			updateCachedTimestamp()
		}
	})
}

func updateCachedTimestamp() {
	now := time.Now().UnixNano()
	atomic.StoreInt64(&cachedTimestamp, now)
	atomic.StoreInt64(&lastUpdate, now)
}

func fastNow() int64 {
	return atomic.LoadInt64(&cachedTimestamp)
}

// fastHash 使用FNV-1a算法快速计算哈希
func fastHash(data []byte) uint32 {
	h := fnv.New32a()
	h.Write(data)
	return h.Sum32()
}

// zeroAllocByteToString 零分配字节转字符串
func zeroAllocByteToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// 高性能entry结构，按缓存行对齐
type fastEntry struct {
	_      [8]byte    // 缓存行填充
	key    string     // 键
	value  []byte     // 值
	expiry int64      // 过期时间戳
	prev   *fastEntry // 双向链表前驱
	next   *fastEntry // 双向链表后继
	_      [8]byte    // 缓存行填充
}

// LRU分片，每个分片独立管理一部分数据
type lruShard struct {
	// 缓存行对齐的互斥锁
	mu sync.RWMutex
	_  [56]byte // 填充到64字节缓存行

	// 数据结构
	cache    map[string]*fastEntry
	head     *fastEntry // 链表头(最新)
	tail     *fastEntry // 链表尾(最旧)
	capacity int
	size     int32 // 原子操作的大小计数

	// 对象池
	entryPool sync.Pool

	// 统计信息
	hits   int64
	misses int64
	_      [40]byte // 缓存行填充
}

// LRUOptimizedHandler 高性能分片LRU缓存处理器
type LRUOptimizedHandler struct {
	shards     []*lruShard
	shardCount uint32
	shardMask  uint32
	maxEntries int
	closed     int32 // 原子操作的关闭标志

	// singleflight用于GetOrCompute防止并发重复计算
	loadGroup sync.Map // map[string]*singleLoadCall
}

// singleLoadCall GetOrCompute的单次调用封装
type singleLoadCall struct {
	wg  sync.WaitGroup
	val []byte
	err error
}

// NewLRUOptimizedHandler 创建分片式高性能LRU缓存
func NewLRUOptimizedHandler(maxEntries int) *LRUOptimizedHandler {
	if maxEntries <= 0 {
		maxEntries = defaultShardCapacity * defaultShardCount
	}

	// 计算分片数量，确保是2的幂次方
	shardCount := defaultShardCount
	if maxEntries < defaultShardCount {
		shardCount = 1
	}
	for shardCount < maxEntries/defaultShardCapacity && shardCount < 256 {
		shardCount <<= 1
	}

	shardCapacity := maxEntries / shardCount
	if shardCapacity < 1 {
		shardCapacity = 1
	}

	h := &LRUOptimizedHandler{
		shards:     make([]*lruShard, shardCount),
		shardCount: uint32(shardCount),
		shardMask:  uint32(shardCount - 1),
		maxEntries: maxEntries,
	}

	// 初始化每个分片
	for i := 0; i < shardCount; i++ {
		h.shards[i] = newLRUShard(shardCapacity)
	}

	return h
}

// newLRUShard 创建新的LRU分片
func newLRUShard(capacity int) *lruShard {
	s := &lruShard{
		cache:    make(map[string]*fastEntry, capacity),
		capacity: capacity,
	}

	// 初始化双向链表
	s.head = &fastEntry{}
	s.tail = &fastEntry{}
	s.head.next = s.tail
	s.tail.prev = s.head

	// 初始化对象池
	s.entryPool = sync.Pool{
		New: func() interface{} {
			return &fastEntry{}
		},
	}

	return s
}

// getShard 根据键获取对应的分片
func (h *LRUOptimizedHandler) getShard(key []byte) *lruShard {
	hash := fastHash(key)
	return h.shards[hash&h.shardMask]
}

// 分片内部方法
func (s *lruShard) getEntry() *fastEntry {
	return s.entryPool.Get().(*fastEntry)
}

func (s *lruShard) putEntry(entry *fastEntry) {
	// 清理entry数据，避免内存泄露
	entry.key = ""
	entry.value = nil
	entry.expiry = 0
	entry.prev = nil
	entry.next = nil
	s.entryPool.Put(entry)
}

// addToHead 将节点添加到链表头部
func (s *lruShard) addToHead(entry *fastEntry) {
	entry.prev = s.head
	entry.next = s.head.next
	s.head.next.prev = entry
	s.head.next = entry
}

// removeEntry 从链表中移除节点
func (s *lruShard) removeEntry(entry *fastEntry) {
	entry.prev.next = entry.next
	entry.next.prev = entry.prev
}

// moveToHead 将节点移动到链表头部
func (s *lruShard) moveToHead(entry *fastEntry) {
	s.removeEntry(entry)
	s.addToHead(entry)
}

// removeTail 移除链表尾部节点
func (s *lruShard) removeTail() *fastEntry {
	if s.tail.prev == s.head {
		return nil
	}
	tail := s.tail.prev
	s.removeEntry(tail)
	return tail
}

// isExpired 检查是否过期
func (s *lruShard) isExpired(expiry int64) bool {
	return expiry > 0 && fastNow() > expiry
}

// ========== 简化版方法（不带context） ==========

// Get 获取缓存值
func (h *LRUOptimizedHandler) Get(key []byte) ([]byte, error) {
	return h.GetWithCtx(context.Background(), key)
}

// GetTTL 获取键的剩余TTL
func (h *LRUOptimizedHandler) GetTTL(key []byte) (time.Duration, error) {
	return h.GetTTLWithCtx(context.Background(), key)
}

// Set 设置缓存值
func (h *LRUOptimizedHandler) Set(key, value []byte) error {
	return h.SetWithCtx(context.Background(), key, value)
}

func (h *LRUOptimizedHandler) SetWithTTL(key, value []byte, ttl time.Duration) error {
	return h.SetWithTTLAndCtx(context.Background(), key, value, ttl)
}

func (h *LRUOptimizedHandler) Del(key []byte) error {
	return h.DelWithCtx(context.Background(), key)
}

func (h *LRUOptimizedHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	return h.BatchGetWithCtx(context.Background(), keys)
}

// ========== 完整版方法（带context） ==========

// GetWithCtx 获取缓存值（支持context）
func (h *LRUOptimizedHandler) GetWithCtx(ctx context.Context, key []byte) ([]byte, error) {
	if key == nil {
		return nil, ErrInvalidKey
	}
	if atomic.LoadInt32(&h.closed) != 0 {
		return nil, ErrClosed
	}

	shard := h.getShard(key)
	sk := zeroAllocByteToString(key)

	// 先用读锁查找
	shard.mu.RLock()
	entry, exists := shard.cache[sk]
	if !exists {
		shard.mu.RUnlock()
		atomic.AddInt64(&shard.misses, 1)
		return nil, ErrNotFound
	}

	// 快速检查过期
	if shard.isExpired(entry.expiry) {
		shard.mu.RUnlock()

		// 升级到写锁进行清理
		shard.mu.Lock()
		if entry, exists := shard.cache[sk]; exists && shard.isExpired(entry.expiry) {
			delete(shard.cache, sk)
			shard.removeEntry(entry)
			atomic.AddInt32(&shard.size, -1)
			shard.putEntry(entry)
		}
		shard.mu.Unlock()
		atomic.AddInt64(&shard.misses, 1)
		return nil, ErrNotFound
	}

	// 复制数据
	result := make([]byte, len(entry.value))
	copy(result, entry.value)
	shard.mu.RUnlock()

	// 升级到写锁更新LRU位置
	shard.mu.Lock()
	if entry, exists := shard.cache[sk]; exists && !shard.isExpired(entry.expiry) {
		shard.moveToHead(entry)
	}
	shard.mu.Unlock()

	atomic.AddInt64(&shard.hits, 1)
	return result, nil
}

// GetTTLWithCtx 获取键的剩余TTL（支持context）
func (h *LRUOptimizedHandler) GetTTLWithCtx(ctx context.Context, key []byte) (time.Duration, error) {
	if key == nil {
		return 0, ErrInvalidKey
	}
	if atomic.LoadInt32(&h.closed) != 0 {
		return 0, ErrClosed
	}

	shard := h.getShard(key)
	sk := zeroAllocByteToString(key)

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	entry, exists := shard.cache[sk]
	if !exists {
		return 0, ErrNotFound
	}

	if shard.isExpired(entry.expiry) {
		return 0, ErrNotFound
	}

	if entry.expiry == 0 {
		return 0, nil // 永不过期
	}

	remaining := entry.expiry - fastNow()
	if remaining <= 0 {
		return 0, ErrNotFound
	}

	return time.Duration(remaining), nil
}

// SetWithCtx 设置缓存（支持context）
func (h *LRUOptimizedHandler) SetWithCtx(ctx context.Context, key, value []byte) error {
	return h.SetWithTTLAndCtx(ctx, key, value, -1)
}

// SetWithTTLAndCtx 设置带TTL的缓存（支持context）
func (h *LRUOptimizedHandler) SetWithTTLAndCtx(ctx context.Context, key, value []byte, ttl time.Duration) error {
	if key == nil {
		return ErrInvalidKey
	}
	if value == nil {
		return ErrInvalidValue
	}
	if ttl < -1 {
		return ErrInvalidTTL
	}
	if atomic.LoadInt32(&h.closed) != 0 {
		return ErrClosed
	}

	shard := h.getShard(key)
	sk := zeroAllocByteToString(key)

	var expiry int64
	if ttl > 0 {
		expiry = fastNow() + ttl.Nanoseconds()
	} else if ttl == 0 {
		expiry = fastNow() - 1 // 立即过期
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()

	// 检查是否已存在
	if entry, exists := shard.cache[sk]; exists {
		// 更新现有条目
		if cap(entry.value) >= len(value) {
			entry.value = entry.value[:len(value)]
			copy(entry.value, value)
		} else {
			entry.value = make([]byte, len(value))
			copy(entry.value, value)
		}
		entry.expiry = expiry
		shard.moveToHead(entry)
		return nil
	}

	// 创建新条目
	entry := shard.getEntry()
	entry.key = string(key)
	entry.value = make([]byte, len(value))
	copy(entry.value, value)
	entry.expiry = expiry

	// 添加到缓存和链表
	shard.cache[sk] = entry
	shard.addToHead(entry)
	atomic.AddInt32(&shard.size, 1)

	// 检查容量限制
	if shard.capacity > 0 && int(atomic.LoadInt32(&shard.size)) > shard.capacity {
		tail := shard.removeTail()
		if tail != nil {
			delete(shard.cache, tail.key)
			atomic.AddInt32(&shard.size, -1)
			shard.putEntry(tail)
		}
	}

	return nil
}

// DelWithCtx 实现Handler.Del
func (h *LRUOptimizedHandler) DelWithCtx(ctx context.Context, key []byte) error {
	if key == nil {
		return ErrInvalidKey
	}
	if atomic.LoadInt32(&h.closed) != 0 {
		return ErrClosed
	}

	shard := h.getShard(key)
	sk := zeroAllocByteToString(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if entry, exists := shard.cache[sk]; exists {
		delete(shard.cache, sk)
		shard.removeEntry(entry)
		atomic.AddInt32(&shard.size, -1)
		shard.putEntry(entry)
	}

	return nil
}

// Close 实现 Handler.Close
func (h *LRUOptimizedHandler) Close() error {
	if !atomic.CompareAndSwapInt32(&h.closed, 0, 1) {
		return nil // 已经关闭
	}

	// 清理所有分片
	for _, shard := range h.shards {
		shard.mu.Lock()
		for k, entry := range shard.cache {
			delete(shard.cache, k)
			shard.removeEntry(entry)
			shard.putEntry(entry)
		}
		atomic.StoreInt32(&shard.size, 0)
		shard.mu.Unlock()
	}

	return nil
}

// BatchGetWithCtx 批量获取，减少锁开销
func (h *LRUOptimizedHandler) BatchGetWithCtx(ctx context.Context, keys [][]byte) ([][]byte, []error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if atomic.LoadInt32(&h.closed) != 0 {
		results := make([][]byte, len(keys))
		errors := make([]error, len(keys))
		for i := range errors {
			errors[i] = ErrClosed
		}
		return results, errors
	}

	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))

	// 按分片分组键值
	type shardBatch struct {
		shard   *lruShard
		indices []int
		keys    []string
	}

	batches := make(map[*lruShard]*shardBatch)

	for i, key := range keys {
		if key == nil {
			errors[i] = ErrInvalidKey
			continue
		}

		shard := h.getShard(key)
		sk := zeroAllocByteToString(key)

		if batches[shard] == nil {
			batches[shard] = &shardBatch{
				shard:   shard,
				indices: make([]int, 0, 4),
				keys:    make([]string, 0, 4),
			}
		}

		batch := batches[shard]
		batch.indices = append(batch.indices, i)
		batch.keys = append(batch.keys, sk)
	}

	// 并发处理各个分片
	var wg sync.WaitGroup
	for _, batch := range batches {
		wg.Add(1)
		b := batch // 避免闭包捕获循环变量
		syncx.Go().OnPanic(nil).Exec(func() {
			defer wg.Done()

			b.shard.mu.RLock()
			defer b.shard.mu.RUnlock()

			for j, sk := range b.keys {
				idx := b.indices[j]

				entry, exists := b.shard.cache[sk]
				if !exists {
					errors[idx] = ErrNotFound
					continue
				}

				if b.shard.isExpired(entry.expiry) {
					errors[idx] = ErrNotFound
					continue
				}

				results[idx] = make([]byte, len(entry.value))
				copy(results[idx], entry.value)
			}
		})
	}

	wg.Wait()
	return results, errors
}

// Stats 返回缓存统计信息
func (h *LRUOptimizedHandler) Stats() map[string]interface{} {
	if atomic.LoadInt32(&h.closed) != 0 {
		return map[string]interface{}{
			"closed": true,
		}
	}

	totalEntries := int32(0)
	totalHits := int64(0)
	totalMisses := int64(0)

	for _, shard := range h.shards {
		totalEntries += atomic.LoadInt32(&shard.size)
		totalHits += atomic.LoadInt64(&shard.hits)
		totalMisses += atomic.LoadInt64(&shard.misses)
	}

	hitRate := float64(0)
	if totalHits+totalMisses > 0 {
		hitRate = float64(totalHits) / float64(totalHits+totalMisses)
	}

	return map[string]interface{}{
		"entries":     totalEntries,
		"max_entries": h.maxEntries,
		"shard_count": h.shardCount,
		"hits":        totalHits,
		"misses":      totalMisses,
		"hit_rate":    hitRate,
		"closed":      false,
	}
}

// GetOrCompute 获取缓存值，如果不存在则计算并设置
func (h *LRUOptimizedHandler) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	ctxLoader := func(context.Context) ([]byte, error) { return loader() }
	return h.GetOrComputeWithCtx(context.Background(), key, ttl, ctxLoader)
}

// GetOrComputeWithCtx 获取或计算值(使用singleflight防止并发重复计算，支持context)
func (h *LRUOptimizedHandler) GetOrComputeWithCtx(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}

	// 首先尝试获取
	if value, err := h.GetWithCtx(ctx, key); err == nil {
		return value, nil
	}

	// 检查context是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// 使用singleflight模式防止并发重复计算
	keyStr := string(key)

	// 快速路径:检查是否已有进行中的调用
	if v, loaded := h.loadGroup.Load(keyStr); loaded {
		sc := v.(*singleLoadCall)
		sc.wg.Wait()
		if sc.err != nil {
			return nil, sc.err
		}
		// 返回拷贝
		result := make([]byte, len(sc.val))
		copy(result, sc.val)
		return result, nil
	}

	// 创建新的调用并预先设置wg
	call := &singleLoadCall{}
	call.wg.Add(1)

	// 尝试存储
	actual, loaded := h.loadGroup.LoadOrStore(keyStr, call)
	if loaded {
		// 其他goroutine已经开始调用,等待结果
		sc := actual.(*singleLoadCall)
		sc.wg.Wait()
		if sc.err != nil {
			return nil, sc.err
		}
		// 返回拷贝
		result := make([]byte, len(sc.val))
		copy(result, sc.val)
		return result, nil
	}

	// 当前goroutine负责执行调用
	defer func() {
		call.wg.Done()
		// 延迟删除
		time.AfterFunc(time.Millisecond*10, func() {
			h.loadGroup.Delete(keyStr)
		})
	}()

	// 再次检查缓存(double-check)
	if value, err := h.GetWithCtx(ctx, key); err == nil {
		call.val = value
		// 返回拷贝
		result := make([]byte, len(value))
		copy(result, value)
		return result, nil
	}

	// 再次检查context
	select {
	case <-ctx.Done():
		call.err = ctx.Err()
		return nil, ctx.Err()
	default:
	}

	// 缓存未命中，调用loader
	value, err := loader(ctx)
	if err != nil {
		call.err = err
		return nil, err
	}

	// 将结果写入缓存
	if ttl <= 0 {
		h.SetWithCtx(ctx, key, value)
	} else {
		h.SetWithTTLAndCtx(ctx, key, value, ttl)
	}

	// 保存结果
	call.val = value

	// 返回值的拷贝
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}
