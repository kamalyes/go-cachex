/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-03 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-03 00:00:00
 * @FilePath: \go-cachex\model_kv_bench_test.go
 * @Description: ModelKVCache 性能/内存/I/O 基准测试
 *
 * 覆盖：
 *  1. extractor 性能对比（unsafe 偏移 vs reflect）—— 证明零反射优化的收益
 *  2. parseSchema 缓存命中 vs 重新解析 —— 证明 schema 缓存的收益
 *  3. Set / Delete 端到端（Redis I/O + 内存分配）—— 测量真实回源开销
 *  4. 并发 Set —— 测量锁竞争与 Redis 连接池开销
 *  5. loadFieldMap —— 测量字段映射构建的内存分配
 *  6. MustGetModelKV —— 注册表查询开销
 *
 * 运行：go test -bench=. -benchmem -count=3 -run=^$ ./...
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// ============================================================
// 基准测试专用 model（字段较多，便于测量内存分配）
// ============================================================

// benchModel 多字段模型，模拟真实 GameBrandModel 规模
type benchModel struct {
	Id        int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Code      string `gorm:"column:code"`
	Name      string `gorm:"column:name"`
	IconUrl   string `gorm:"column:icon_url"`
	Enabled   bool   `gorm:"column:enabled"`
	SortOrder int32  `gorm:"column:sort_order"`
}

func (benchModel) TableName() string { return "bench_model" }

// ============================================================
// 反射版 extractor（用于与 unsafe 版本对比，证明优化收益）
// ============================================================

// reflectExtractor 纯 reflect 实现的字段提取器（对照基准）
func reflectExtractor[M any](fieldName string) func(*M) string {
	return func(m *M) string {
		if m == nil {
			return ""
		}
		v := reflect.ValueOf(m).Elem().FieldByName(fieldName)
		switch v.Kind() {
		case reflect.String:
			return v.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.FormatInt(v.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.FormatUint(v.Uint(), 10)
		case reflect.Bool:
			return strconv.FormatBool(v.Bool())
		default:
			return fmt.Sprintf("%v", v.Interface())
		}
	}
}

// unsafeStringExtractor 手写 unsafe string 提取器（与 buildExtractor 内部逻辑一致，用于隔离测量）
func unsafeStringExtractor[M any](m *M, offset uintptr) string {
	if m == nil {
		return ""
	}
	return *(*string)(unsafe.Pointer(uintptr(unsafe.Pointer(m)) + offset))
}

// ============================================================
// 1. extractor 性能对比（不依赖 Redis）
// ============================================================

// BenchmarkExtractor_String_Unsafe unsafe 偏移读取 string 字段
func BenchmarkExtractor_String_Unsafe(b *testing.B) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[benchModel](db)
	require.NoError(b, err)
	sf := s.LookUpField("Name")
	require.NotNil(b, sf)
	extractor := buildExtractor[benchModel](sf)

	m := &benchModel{Id: 1, Name: "brand-name"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractor(m)
	}
}

// BenchmarkExtractor_String_Reflect reflect 读取 string 字段（对照）
func BenchmarkExtractor_String_Reflect(b *testing.B) {
	extractor := reflectExtractor[benchModel]("Name")
	m := &benchModel{Id: 1, Name: "brand-name"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractor(m)
	}
}

// BenchmarkExtractor_Int64_Unsafe unsafe 偏移读取 int64 主键
func BenchmarkExtractor_Int64_Unsafe(b *testing.B) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[benchModel](db)
	require.NoError(b, err)
	sf := s.LookUpField("Id")
	require.NotNil(b, sf)
	extractor := buildExtractor[benchModel](sf)

	m := &benchModel{Id: 9999999}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractor(m)
	}
}

// BenchmarkExtractor_Int64_Reflect reflect 读取 int64 主键（对照）
func BenchmarkExtractor_Int64_Reflect(b *testing.B) {
	extractor := reflectExtractor[benchModel]("Id")
	m := &benchModel{Id: 9999999}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractor(m)
	}
}

// BenchmarkExtractor_UnsafeInline 内联 unsafe 调用（无函数指针间接跳转，测量上限）
func BenchmarkExtractor_UnsafeInline(b *testing.B) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[benchModel](db)
	require.NoError(b, err)
	sf := s.LookUpField("Name")
	require.NotNil(b, sf)
	offset := sf.StructField.Offset

	m := &benchModel{Id: 1, Name: "brand-name"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = unsafeStringExtractor(m, offset)
	}
}

// ============================================================
// 2. parseSchema 缓存命中 vs 重新解析（不依赖 Redis）
// ============================================================

// BenchmarkParseSchema_Cached schema 缓存命中路径
func BenchmarkParseSchema_Cached(b *testing.B) {
	db := newSchemaOnlyDB()
	// 预热缓存
	_, err := parseSchema[benchModel](db)
	require.NoError(b, err)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseSchema[benchModel](db)
	}
}

// BenchmarkParseSchema_Fresh 每次清缓存重新解析（最坏情况）
func BenchmarkParseSchema_Fresh(b *testing.B) {
	db := newSchemaOnlyDB()
	t := reflect.TypeOf(benchModel{})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		schemaCache.Delete(t)
		_, _ = parseSchema[benchModel](db)
	}
}

// ============================================================
// 3. Set / Delete 端到端（Redis I/O + 内存分配）
// ============================================================

// setupBenchModelKV 基准测试专用注册：每个 b.N 前注册一次，返回 cache 与 client
func setupBenchModelKV(b *testing.B, fieldCount int) (*ModelKVCache[benchModel], func()) {
	b.Helper()
	client := setupRedisClient(b)
	if client == nil {
		b.Skip("Redis 不可用，跳过依赖 Redis 的基准测试")
	}
	SetGlobalRedisClient(client)
	ResetModelKVRegistry()
	resetKVRegistry()

	// 清理上次残留
	_ = client.FlushDB(context.Background()).Err()

	base := NewModelKVBase()
	builder := NewModelKV[benchModel](base, newSchemaOnlyDB())
	switch fieldCount {
	case 1:
		builder = builder.Field("Name")
	case 3:
		builder = builder.
			Field("Name").
			Field("Code").
			Field("IconUrl")
	default:
		builder = builder.Field("Name")
	}
	cache := builder.Register()

	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		client.Close()
	}
	return cache, cleanup
}

// BenchmarkModelKVCache_Set_SingleField 单字段 Set（1 次 Redis HSET + 1 次广播）
func BenchmarkModelKVCache_Set_SingleField(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := &benchModel{Id: int64(i), Name: fmt.Sprintf("n%d", i)}
		if err := cache.Set(ctx, m); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkModelKVCache_Set_ThreeFields 三字段 Set（3 次 Redis HSET + 3 次广播）
func BenchmarkModelKVCache_Set_ThreeFields(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 3)
	defer cleanup()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := &benchModel{
			Id:      int64(i),
			Name:    fmt.Sprintf("n%d", i),
			Code:    fmt.Sprintf("c%d", i),
			IconUrl: fmt.Sprintf("http://x/%d", i),
		}
		if err := cache.Set(ctx, m); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkModelKVCache_Delete 单字段 Delete（1 次 Redis HDEL + 1 次广播）
func BenchmarkModelKVCache_Delete(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()
	ctx := context.Background()

	// 预写 N 条
	models := make([]*benchModel, b.N)
	for i := 0; i < b.N; i++ {
		m := &benchModel{Id: int64(i), Name: fmt.Sprintf("n%d", i)}
		models[i] = m
		if err := cache.Set(ctx, m); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cache.Delete(ctx, models[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkModelKVCache_DeleteByKey 按 key 删除（无需 model 实例）
func BenchmarkModelKVCache_DeleteByKey(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		m := &benchModel{Id: int64(i), Name: fmt.Sprintf("n%d", i)}
		if err := cache.Set(ctx, m); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cache.DeleteByKey(ctx, strconv.FormatInt(int64(i), 10)); err != nil {
			b.Fatal(err)
		}
	}
}

// ============================================================
// 4. 并发 Set（测量锁竞争 + Redis 连接池开销）
// ============================================================

// BenchmarkModelKVCache_SetParallel 并发 Set（多 goroutine 抢同一 cache）
func BenchmarkModelKVCache_SetParallel(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()
	ctx := context.Background()

	var counter int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := atomic.AddInt64(&counter, 1)
		for pb.Next() {
			m := &benchModel{Id: id, Name: fmt.Sprintf("n%d", id)}
			if err := cache.Set(ctx, m); err != nil {
				b.Fatal(err)
			}
			id = atomic.AddInt64(&counter, 1)
		}
	})
}

// BenchmarkModelKVCache_SetParallel_ThreeFields 并发三字段 Set
func BenchmarkModelKVCache_SetParallel_ThreeFields(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 3)
	defer cleanup()
	ctx := context.Background()

	var counter int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := atomic.AddInt64(&counter, 1)
		for pb.Next() {
			m := &benchModel{
				Id:   id,
				Name: fmt.Sprintf("n%d", id),
				Code: fmt.Sprintf("c%d", id),
			}
			if err := cache.Set(ctx, m); err != nil {
				b.Fatal(err)
			}
			id = atomic.AddInt64(&counter, 1)
		}
	})
}

// ============================================================
// 5. loadFieldMap（字段映射构建，绕过真实 DB）
// ============================================================

// BenchmarkLoadFieldMap 构建 1000 条记录的字段映射
func BenchmarkLoadFieldMap(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()

	// 预构造 1000 条 cachedItems，绕过 DB 查询
	// 延长 loadTTL 避免基准运行期间缓存过期触发回源 panic
	cache.loadTTL = time.Hour
	items := make([]*benchModel, 1000)
	for i := range items {
		items[i] = &benchModel{Id: int64(i), Name: fmt.Sprintf("name-%d", i)}
	}
	cache.cacheMu.Lock()
	cache.cachedItems = items
	cache.cachedItemsAt = time.Now()
	cache.cacheMu.Unlock()

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cache.loadFieldMap(ctx, 0)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoadFieldMap_ThreeFields 三字段映射构建（1000 条）
func BenchmarkLoadFieldMap_ThreeFields(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 3)
	defer cleanup()

	cache.loadTTL = time.Hour
	items := make([]*benchModel, 1000)
	for i := range items {
		items[i] = &benchModel{
			Id:      int64(i),
			Name:    fmt.Sprintf("name-%d", i),
			Code:    fmt.Sprintf("code-%d", i),
			IconUrl: fmt.Sprintf("http://x/%d", i),
		}
	}
	cache.cacheMu.Lock()
	cache.cachedItems = items
	cache.cachedItemsAt = time.Now()
	cache.cacheMu.Unlock()

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 3; j++ {
			_, err := cache.loadFieldMap(ctx, j)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// ============================================================
// 6. MustGetModelKV 注册表查询（不依赖 Redis 业务路径）
// ============================================================

// BenchmarkMustGetModelKV 注册表查询开销（reflect.Type 索引 map）
func BenchmarkMustGetModelKV(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()
	_ = cache

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MustGetModelKV[benchModel]()
	}
}

// BenchmarkGetModelKV 带 error 返回的查询（对比 Must 版本）
func BenchmarkGetModelKV(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()
	_ = cache

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GetModelKV[benchModel]()
	}
}

// ============================================================
// 7. 内存开销专项：单次 Set 的分配来源
// ============================================================

// BenchmarkModelKVCache_Set_AllocDetail 单次 Set 内存分配详情
// 通过 -benchmem 观察 B/op 与 allocs/op，评估每次 Set 的 GC 压力
func BenchmarkModelKVCache_Set_AllocDetail(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 3)
	defer cleanup()
	ctx := context.Background()
	m := &benchModel{Id: 1, Name: "n", Code: "c", IconUrl: "u"}

	// 预热（首次 Set 可能触发 PubSub 订阅等一次性分配）
	_ = cache.Set(ctx, m)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.Set(ctx, m)
	}
}

// BenchmarkModelKVCache_KeyExtractor 单独测量主键提取（unsafe 路径，Set 内每条必调）
func BenchmarkModelKVCache_KeyExtractor(b *testing.B) {
	cache, cleanup := setupBenchModelKV(b, 1)
	defer cleanup()

	m := &benchModel{Id: 12345, Name: "x"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.keyExtractor(m)
	}
}
