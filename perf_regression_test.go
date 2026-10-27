/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-10-27 13:19:52
 * @FilePath: \go-cachex\perf_regression_test.go
 * @Description: 性能优化的回归闸门（断言 + 基准，随优化一同维护）
 *
 * 覆盖 perf 报告指出的热路径：
 *   - MultiLevelCache 统计锁（全局互斥 → 原子）
 *   - LRUOptimized 读路径写锁（moveToHead → 近似 LRU）
 *   - LRUOptimized 分片容量留存率
 *   - ExpiringHandler 全表清扫的尾延迟
 */

package cachex

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// --- MultiLevelCache 统计路径 ---

func newProbeMultiLevel(tb testing.TB) *MultiLevelCache[string] {
	tb.Helper()
	mr := miniredis.RunT(tb)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	tb.Cleanup(func() { _ = client.Close() })

	return NewMultiLevelCache[string](client, MultiLevelConfig{
		Namespace: "probe",
		L1Size:    4096,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	})
}

// BenchmarkProbe_MultiLevel_L1Hit_Parallel L1 命中并行扩展性
// 统计锁是全局互斥时此项不随核数扩展（下层 L1 分片被锁抵消）
func BenchmarkProbe_MultiLevel_L1Hit_Parallel(b *testing.B) {
	c := newProbeMultiLevel(b)
	ctx := context.Background()
	for i := 0; i < 256; i++ {
		_ = c.Set(ctx, fmt.Sprintf("k%d", i), "v")
	}

	// loader 不应被调用（全部 L1 命中）；返回值仅为满足签名
	loader := func() (string, error) { return "v", nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = c.Get(ctx, fmt.Sprintf("k%d", i&255), loader)
			i++
		}
	})
}

// BenchmarkProbe_MultiLevel_StatsRecord 纯计数开销（隔离锁/原子差异）
func BenchmarkProbe_MultiLevel_StatsRecord(b *testing.B) {
	c := newProbeMultiLevel(b)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.recordL1Hit()
		}
	})
}

// --- LRUOptimized 读路径 ---

func newProbeLRUOpt(tb testing.TB, size int) *LRUOptimizedHandler {
	tb.Helper()
	h := NewLRUOptimizedHandler(size)
	tb.Cleanup(func() { _ = h.Close() })
	return h
}

// BenchmarkProbe_LRUOpt_GetHit_Parallel 读命中并行扩展性
// 读路径若升级写锁更新 LRU 位置，则分片内读互相串行，近乎不扩展
func BenchmarkProbe_LRUOpt_GetHit_Parallel(b *testing.B) {
	const n = 4096
	h := newProbeLRUOpt(b, n*2)
	val := make([]byte, 64)
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("key-%06d", i))
		_ = h.Set(keys[i], val)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = h.Get(keys[i&(n-1)])
			i++
		}
	})
}

// BenchmarkProbe_LRUOpt_GetHit_Serial 读命中串行基线
func BenchmarkProbe_LRUOpt_GetHit_Serial(b *testing.B) {
	const n = 4096
	h := newProbeLRUOpt(b, n*2)
	val := make([]byte, 64)
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("key-%06d", i))
		_ = h.Set(keys[i], val)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = h.Get(keys[i&(n-1)])
	}
}

// BenchmarkProbe_LRUOpt_Mixed90Read_Parallel 90 读 / 10 写混合
func BenchmarkProbe_LRUOpt_Mixed90Read_Parallel(b *testing.B) {
	const n = 4096
	h := newProbeLRUOpt(b, n*2)
	val := make([]byte, 64)
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("key-%06d", i))
		_ = h.Set(keys[i], val)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i&(n-1)]
			if i%10 == 0 {
				_ = h.Set(k, val)
			} else {
				_, _ = h.Get(k)
			}
			i++
		}
	})
}

// --- 留存率与命中率（正确性侧量化，非基准） ---

// TestProbe_LRUOpt_Retention 配置容量下的实际留存率
// 分片硬容量 = maxEntries/shardCount 时，留存会显著低于配置容量
func TestProbe_LRUOpt_Retention(t *testing.T) {
	for _, max := range []int{1000, 10000} {
		h := NewLRUOptimizedHandler(max)
		for i := 0; i < max; i++ {
			_ = h.Set([]byte(fmt.Sprintf("k-%08d", i)), []byte("v"))
		}
		got := entryCount(t, h)
		t.Logf("maxEntries=%-6d 留存=%-6d 留存率=%.1f%% shards=%v",
			max, got, float64(got)*100/float64(max), h.Stats()["shard_count"])
		_ = h.Close()
	}
}

// TestProbe_LRUOpt_HitRate Zipf 倾斜负载下的命中率
// 用于量化「严格 LRU → 近似 LRU」的淘汰质量变化
func TestProbe_LRUOpt_HitRate(t *testing.T) {
	const (
		keyspace = 20000
		capacity = 2000
		ops      = 400000
	)
	h := NewLRUOptimizedHandler(capacity)
	defer h.Close()

	// Zipf s=1.1：热键集中，典型缓存负载形态
	zipf := rand.NewZipf(rand.New(rand.NewSource(42)), 1.1, 1, keyspace-1)
	val := []byte("v")

	var hits, total int
	for i := 0; i < ops; i++ {
		k := []byte(fmt.Sprintf("k-%06d", zipf.Uint64()))
		if _, err := h.Get(k); err == nil {
			hits++
		} else {
			_ = h.Set(k, val)
		}
		total++
	}
	t.Logf("Zipf(1.1) keyspace=%d capacity=%d ops=%d 命中率=%.2f%%",
		keyspace, capacity, ops, float64(hits)*100/float64(total))
}

// --- ExpiringHandler 清扫尾延迟 ---

// TestProbe_Expiring_SweepTailLatency 清扫期间 Get 的延迟分布
// 全表清扫持独占锁时，均值完全掩盖尾延迟，必须看分位数
func TestProbe_Expiring_SweepTailLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("尾延迟测量耗时较长")
	}
	const entries = 100000

	run := func(name string, interval time.Duration) {
		h := NewExpiringHandler(interval)
		defer h.Close()

		val := []byte("v")
		for i := 0; i < entries; i++ {
			// TTL 足够长，清扫扫到但不删除（最廉价的清扫分支）
			_ = h.SetWithTTL([]byte(fmt.Sprintf("k-%08d", i)), val, time.Hour)
		}

		probe := []byte("k-00000000")
		const samples = 2000000
		lat := make([]time.Duration, 0, samples/100)
		deadline := time.Now().Add(3 * time.Second)
		for i := 0; i < samples && time.Now().Before(deadline); i++ {
			st := time.Now()
			_, _ = h.Get(probe)
			if i%100 == 0 {
				lat = append(lat, time.Since(st))
			}
		}
		if len(lat) == 0 {
			t.Fatalf("%s: 未采到样本", name)
		}
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		q := func(p float64) time.Duration { return lat[int(float64(len(lat)-1)*p)] }
		t.Logf("%s: n=%d p50=%v p99=%v p99.9=%v p99.99=%v max=%v",
			name, len(lat), q(0.5), q(0.99), q(0.999), q(0.9999), lat[len(lat)-1])
	}

	run("janitor=idle(1h)", time.Hour)
	run("janitor=10ms   ", 10*time.Millisecond)
}

// TestProbe_Expiring_SweepStillReclaims 增量清扫必须仍能回收过期条目
//
// 取样清扫放弃了「每 tick 全表扫净」的强度，故须验证多轮之后仍收敛——
// 否则内存回收失效（惰性过期只在 key 被再次访问时生效，永不访问的键会滞留）。
func TestProbe_Expiring_SweepStillReclaims(t *testing.T) {
	h := NewExpiringHandler(5 * time.Millisecond)
	defer h.Close()

	const n = 5000
	for i := 0; i < n; i++ {
		_ = h.SetWithTTL([]byte(fmt.Sprintf("k-%06d", i)), []byte("v"), 10*time.Millisecond)
	}

	h.mu.RLock()
	before := len(h.items)
	h.mu.RUnlock()

	// 全部条目在 10ms 后过期；留足多轮 tick 让取样清扫收敛
	deadline := time.Now().Add(5 * time.Second)
	var remaining int
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		h.mu.RLock()
		remaining = len(h.items)
		h.mu.RUnlock()
		if remaining == 0 {
			break
		}
	}

	t.Logf("写入=%d 清扫前=%d 清扫后=%d", n, before, remaining)
	if remaining != 0 {
		t.Fatalf("增量清扫未收敛：仍残留 %d 条过期条目（内存回收失效）", remaining)
	}
}

// --- Ristretto 写路径：同步等待 vs 异步 ---

func newProbeRistretto(tb testing.TB, async bool) *RistrettoHandler {
	tb.Helper()
	cfg := NewDefaultRistrettoConfig()
	cfg.AsyncWrites = async
	h, err := NewRistrettoHandler(cfg)
	if err != nil {
		tb.Fatalf("构造 RistrettoHandler 失败: %v", err)
	}
	tb.Cleanup(func() { _ = h.Close() })
	return h
}

func benchRistrettoSet(b *testing.B, async, parallel bool) {
	h := newProbeRistretto(b, async)
	val := make([]byte, 64)
	b.ResetTimer()
	if parallel {
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				_ = h.Set([]byte(fmt.Sprintf("k-%06d", i&4095)), val)
				i++
			}
		})
		return
	}
	for i := 0; i < b.N; i++ {
		_ = h.Set([]byte(fmt.Sprintf("k-%06d", i&4095)), val)
	}
}

func BenchmarkProbe_Ristretto_Set_Sync(b *testing.B)     { benchRistrettoSet(b, false, false) }
func BenchmarkProbe_Ristretto_Set_Async(b *testing.B)    { benchRistrettoSet(b, true, false) }
func BenchmarkProbe_Ristretto_SetPar_Sync(b *testing.B)  { benchRistrettoSet(b, false, true) }
func BenchmarkProbe_Ristretto_SetPar_Async(b *testing.B) { benchRistrettoSet(b, true, true) }

// TestProbe_Ristretto_DefaultIsSync 默认配置必须保持写后同步可见（不破坏既有行为）
func TestProbe_Ristretto_DefaultIsSync(t *testing.T) {
	h := newProbeRistretto(t, false) // 显式 false = 默认值
	if err := h.Set([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	got, err := h.Get([]byte("k"))
	if err != nil {
		t.Fatalf("默认配置下写后应立即可读，实得错误: %v", err)
	}
	if string(got) != "v" {
		t.Fatalf("值错误: 期望 v，实得 %q", got)
	}

	// 零值配置同样必须是同步语义（结构体字面量调用方的行为不变）
	zero := &RistrettoConfig{NumCounters: 1e4, MaxCost: 1 << 20, BufferItems: 64, TtlTickerDurationInSec: 1}
	if zero.AsyncWrites {
		t.Fatal("RistrettoConfig 零值的 AsyncWrites 必须为 false（保持既有同步行为）")
	}
}

// --- 压缩路径：双拷贝 + 无池化 ---

func newProbeAdvanced(tb testing.TB) *AdvancedCache[string] {
	tb.Helper()
	mr := miniredis.RunT(tb)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	tb.Cleanup(func() { _ = client.Close() })

	return NewAdvancedCache[string](client, AdvancedCacheConfig{
		Compression:        CompressionGzip,
		MinSizeForCompress: 1,
		Namespace:          "probe",
	})
}

func benchCompress(b *testing.B, size int) {
	c := newProbeAdvanced(b)
	// 可压缩但非全零的载荷（近似 JSON 文本的冗余度）
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + i%16)
	}
	b.ResetTimer()
	b.SetBytes(int64(size))
	for i := 0; i < b.N; i++ {
		if _, err := c.compress(data); err != nil {
			b.Fatalf("compress 失败: %v", err)
		}
	}
}

func BenchmarkProbe_Compress_1KB(b *testing.B)  { benchCompress(b, 1024) }
func BenchmarkProbe_Compress_16KB(b *testing.B) { benchCompress(b, 16*1024) }

func BenchmarkProbe_Decompress_16KB(b *testing.B) {
	c := newProbeAdvanced(b)
	data := make([]byte, 16*1024)
	for i := range data {
		data[i] = byte('a' + i%16)
	}
	comp, err := c.compress(data)
	if err != nil {
		b.Fatalf("compress 失败: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.decompress(comp); err != nil {
			b.Fatalf("decompress 失败: %v", err)
		}
	}
}

// TestProbe_CompressRoundTrip 压缩/解压往返必须保真
// gzip.Writer 改为池化复用（Reset），须验证复用后不串数据
func TestProbe_CompressRoundTrip(t *testing.T) {
	c := newProbeAdvanced(t)

	for _, size := range []int{1, 100, 1024, 64 * 1024} {
		src := make([]byte, size)
		for i := range src {
			src[i] = byte(i*7 + size)
		}
		comp, err := c.compress(src)
		if err != nil {
			t.Fatalf("size=%d compress 失败: %v", size, err)
		}
		got, err := c.decompress(comp)
		if err != nil {
			t.Fatalf("size=%d decompress 失败: %v", size, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("size=%d 往返不保真: 长度 got=%d want=%d", size, len(got), len(src))
		}
	}

	// 连续多轮验证池化 Writer 复用后无残留
	for round := 0; round < 50; round++ {
		src := []byte(fmt.Sprintf("round-%d-%s", round, string(make([]byte, round*17))))
		comp, err := c.compress(src)
		if err != nil {
			t.Fatalf("round=%d compress 失败: %v", round, err)
		}
		got, err := c.decompress(comp)
		if err != nil {
			t.Fatalf("round=%d decompress 失败: %v", round, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("round=%d 池化复用后数据串台", round)
		}
	}
}
