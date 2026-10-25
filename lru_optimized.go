/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 22:30:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-10-25 11:07:52
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
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/kamalyes/go-toolbox/pkg/mathx"
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
	// clockOnce 保证刷新协程至多启动一次，且仅在首个 handler 构造时启动
	clockOnce sync.Once
)

// 初始化时间戳缓存
//
// 仅置初值，不启协程：原实现在 init 中无条件起一个 10ms ticker 协程，
// 于是**任何** import 本包的程序（6 个服务 + go-wsc）都永久承担 100 次/秒的定时器唤醒，
// 即便从未构造 LRUOptimizedHandler，且无从关闭——既干扰运行时空闲状态，
// 也会计入容器 CPU 账目。改为由 startClock 在首个 handler 构造时惰性启动。
func init() {
	updateCachedTimestamp()
}

// startClock 惰性启动时间戳刷新协程（仅首个 handler 构造时真正执行一次）
//
// 保留时间戳缓存而非直接调用 time.Now：本机实测 fastNow 为 0.25ns，
// time.Now().UnixNano() 为 28–31ns，而 GetHit 基线约 50ns——
// 去掉缓存会使读命中劣化约 60%，缓存的收益是 100 倍量级，必须保留。
// 代价是 TTL 判定存在至多 timestampCacheInterval（10ms）的滞后，属既有行为，不变更。
func startClock() {
	clockOnce.Do(func() {
		updateCachedTimestamp()
		syncx.Go().OnPanic(nil).Exec(func() {
			ticker := time.NewTicker(time.Millisecond * timestampCacheInterval)
			defer ticker.Stop()
			for range ticker.C {
				updateCachedTimestamp()
			}
		})
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

	// accessed CLOCK 访问位（近似 LRU）
	//
	// 读命中仅原子置 1，不触碰链表——这让读路径真正只读（只持 RLock）。
	// 淘汰时（持写锁）从尾部扫描：置位者清零并给第二次机会，未置位者即victim。
	// 必须用原子访问：读命中在 RLock 下并发写入本字段，非原子写会被 race 检测捕获。
	accessed atomic.Uint32

	_ [8]byte // 缓存行填充
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
	maxEntries = mathx.IfLeZero(maxEntries, defaultShardCapacity*defaultShardCount)

	// 计算分片数量，确保是2的幂次方
	shardCount := defaultShardCount
	if maxEntries < defaultShardCount {
		shardCount = 1
	}
	// 分片数上界取 4×GOMAXPROCS（向上取 2 的幂）而非 maxEntries/64 封顶 256。
	//
	// 分片容量是**硬上限**，而键的哈希分布不可能完全均匀，故单分片容量越小、
	// 相对方差越大，越容易在整体远未装满时就触发局部淘汰。
	// 旧算法在 maxEntries=10000 时得到 256 个分片 × 39 容量，实测仅留存 87.2%
	// （分片占用 min 21 / max 39），且淘汰按分片内局部新近度而非全局新近度进行。
	// 分片数的真实作用是降低锁竞争，其收益在约 4×CPU 核数后饱和，
	// 继续增加只会放大容量碎片，故在此封顶。
	maxShards := 4 * runtime.GOMAXPROCS(0)
	shardCap := defaultShardCount
	for shardCap < maxShards && shardCap < 256 {
		shardCap <<= 1
	}
	for shardCount < maxEntries/defaultShardCapacity && shardCount < shardCap {
		shardCount <<= 1
	}

	// 向上取整分配分片容量：下取整会丢弃余数，使各分片容量之和小于 maxEntries
	shardCapacity := (maxEntries + shardCount - 1) / shardCount
	if shardCapacity < 1 {
		shardCapacity = 1
	}

	// 时间戳刷新协程在首个 handler 构造时惰性启动（见 startClock）
	startClock()

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
	entry.accessed.Store(0) // 复用前清零访问位，避免继承上一轮的第二次机会
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

// evictVictim 选取并摘除淘汰目标（CLOCK 第二次机会，近似 LRU）
//
// 调用方必须持写锁。自尾部向头部扫描：
//   - 访问位为 1：清零并移到头部（给第二次机会），继续向前找
//   - 访问位为 0：即为 victim，摘除返回
//
// 扫描步数以当前 size 为上界：若全部条目都带访问位（极热负载），
// 退化为无条件淘汰当前尾节点，保证本函数必定终止且必定腾出一个名额。
// 相比严格 LRU，淘汰精度换取了读路径的无写锁化——淘汰质量以 Zipf 负载命中率验证。
func (s *lruShard) evictVictim() *fastEntry {
	for steps := int(atomic.LoadInt32(&s.size)); steps > 0; steps-- {
		victim := s.tail.prev
		if victim == s.head {
			return nil // 空链表
		}
		if victim.accessed.CompareAndSwap(1, 0) {
			// 命中过：清零访问位并给第二次机会
			s.moveToHead(victim)
			continue
		}
		s.removeEntry(victim)
		return victim
	}

	// 全员带访问位：退化为淘汰尾节点，确保腾出名额
	return s.removeTail()
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

	// 置 CLOCK 访问位代替「升级写锁 + moveToHead」
	//
	// 原实现在此 RUnlock 后再取**写锁**、二次查 map、moveToHead——
	// 纯读操作却付两次加锁 + 两次 map 查找，且写锁使同分片内的读彼此串行，
	// per-shard RWMutex 在命中路径上形同普通 Mutex（实测 10 核仅 1.38× 扩展）。
	// 现在命中路径只持 RLock 并做一次原子置位，分片内读可真正并发；
	// 链表顺序的维护下沉到淘汰时的第二次机会扫描（evictVictim）。
	entry.accessed.Store(1)
	shard.mu.RUnlock()

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
		entry.accessed.Store(1) // 覆写视为访问
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
	//
	// ⚠️ 键必须用 entry.key（:417 的 string(key) 安全拷贝），不可用 sk：
	// sk 是 zeroAllocByteToString 产出的 unsafe 别名，其字符串头指向调用方的 []byte。
	// 查找/删除用别名安全（map 按值比较，别名在调用期内有效），但**存储**会让 map 永久
	// 持有指向调用方缓冲区的指针——调用方复用该缓冲区（池化热路径的常规做法）时，
	// map 里的键原地变异，条目落在错误的哈希桶：既无法按任何键查到，也无法按 entry.key 删除，
	// 继而 putEntry 把它归还 sync.Pool 并置空 prev/next，而损坏的键仍能解析到该池化条目，
	// 最终在 removeEntry 空指针解引用——整进程 SIGSEGV。
	// 其余 handler（lru.go / expiring.go）均按 string(key) 拷贝存储，此处对齐同一契约。
	// 本缺陷 -race 检测不到（内存别名，非数据竞争），只能靠复用缓冲区的回归测试覆盖。
	shard.cache[entry.key] = entry
	shard.addToHead(entry)
	atomic.AddInt32(&shard.size, 1)

	// 检查容量限制（CLOCK 第二次机会选 victim，近似 LRU）
	if shard.capacity > 0 && int(atomic.LoadInt32(&shard.size)) > shard.capacity {
		victim := shard.evictVictim()
		if victim != nil {
			delete(shard.cache, victim.key)
			atomic.AddInt32(&shard.size, -1)
			shard.putEntry(victim)
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

// Clear 清空所有缓存条目但不关闭 handler，使其可以继续使用
// 线程安全，可在并发访问时调用
func (h *LRUOptimizedHandler) Clear() {
	if atomic.LoadInt32(&h.closed) != 0 {
		return
	}

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
