/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 00:00:00
 * @FilePath: \go-cachex\hotkey_test.go
 * @Description: 热key缓存测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHotKeyCache_BasicOperations(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 创建数据加载器
	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{
				1: "张三",
				2: "李四",
				3: "王五",
			}, nil
		},
	}

	// 创建热key缓存
	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false, // 测试中禁用自动刷新
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "user_names", loader, config)
	defer cache.Stop()

	// 测试获取单个值（首次加载）
	name, exists, err := cache.Get(ctx, 1)
	assert.NoError(t, err)
	assert.True(t, exists, "用户1应该存在")
	assert.Equal(t, "张三", name, "用户名应该是张三")

	// 测试获取不存在的值
	name, exists, err = cache.Get(ctx, 999)
	assert.NoError(t, err)
	assert.False(t, exists, "用户999不应该存在")

	// 测试获取所有值
	allNames, err := cache.GetAll(ctx)
	assert.NoError(t, err)
	assert.Len(t, allNames, 3, "应该有3个用户")
	assert.Equal(t, "李四", allNames[2])
	assert.Equal(t, "王五", allNames[3])

	// 测试设置新值
	err = cache.Set(ctx, 4, "赵六")
	assert.NoError(t, err)

	// 验证新值
	name, exists, err = cache.Get(ctx, 4)
	assert.NoError(t, err)
	assert.True(t, exists, "新添加的用户4应该存在")
	assert.Equal(t, "赵六", name)

	// 测试批量设置
	newUsers := map[int]string{
		5: "孙七",
		6: "周八",
	}
	err = cache.SetAll(ctx, newUsers)
	assert.NoError(t, err)

	// 验证批量设置的值
	allNames, err = cache.GetAll(ctx)
	assert.NoError(t, err)
	assert.Len(t, allNames, 2, "SetAll应该替换所有数据")
	assert.Equal(t, "孙七", allNames[5])
	assert.Equal(t, "周八", allNames[6])
}

func TestHotKeyCache_Delete(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 创建测试数据
	loader := &SQLDataLoader[string, int]{
		QueryFunc: func(ctx context.Context) (map[string]int, error) {
			return map[string]int{
				"apple":  1,
				"banana": 2,
				"orange": 3,
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[string, int](client, "fruits", loader, config)
	defer cache.Stop()

	// 加载初始数据
	_, err := cache.GetAll(ctx)
	assert.NoError(t, err)

	// 删除一个键
	err = cache.Delete(ctx, "banana")
	assert.NoError(t, err)

	// 验证删除效果
	value, exists, err := cache.Get(ctx, "banana")
	assert.NoError(t, err)
	assert.False(t, exists, "删除的键不应该存在")
	assert.Zero(t, value, "删除的键应该返回零值")

	// 确认其他键仍然存在
	value, exists, err = cache.Get(ctx, "apple")
	assert.NoError(t, err)
	assert.True(t, exists, "其他键应该仍然存在")
	assert.Equal(t, 1, value)
}

func TestHotKeyCache_Exists(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{
				100: "测试数据",
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_exists", loader, config)
	defer cache.Stop()

	// 测试存在的键
	exists, err := cache.Exists(ctx, 100)
	assert.NoError(t, err)
	assert.True(t, exists, "键100应该存在")

	// 测试不存在的键
	exists, err = cache.Exists(ctx, 200)
	assert.NoError(t, err)
	assert.False(t, exists, "键200不应该存在")
}

func TestHotKeyCache_Keys(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[string, int]{
		QueryFunc: func(ctx context.Context) (map[string]int, error) {
			return map[string]int{
				"key1": 1,
				"key2": 2,
				"key3": 3,
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[string, int](client, "test_keys", loader, config)
	defer cache.Stop()

	// 获取所有键
	keys, err := cache.Keys(ctx)
	assert.NoError(t, err)
	assert.Len(t, keys, 3, "应该有3个键")

	// 验证键的内容
	expectedKeys := []string{"key1", "key2", "key3"}
	for _, expectedKey := range expectedKeys {
		assert.Contains(t, keys, expectedKey, fmt.Sprintf("应该包含键%s", expectedKey))
	}
}

func TestHotKeyCache_Size(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{
				1: "a",
				2: "b",
				3: "c",
				4: "d",
				5: "e",
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_size", loader, config)
	defer cache.Stop()

	// 获取大小
	size, err := cache.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 5, size, "缓存大小应该是5")

	// 删除一个键后重新检查大小
	err = cache.Delete(ctx, 1)
	assert.NoError(t, err)

	size, err = cache.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 4, size, "删除后缓存大小应该是4")
}

func TestHotKeyCache_Clear(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[string, string]{
		QueryFunc: func(ctx context.Context) (map[string]string, error) {
			return map[string]string{
				"test1": "value1",
				"test2": "value2",
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[string, string](client, "test_clear", loader, config)
	defer cache.Stop()

	// 先触发数据加载
	_, err := cache.GetAll(ctx)
	assert.NoError(t, err)

	// 确保有数据
	size, err := cache.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, size)

	// 清空缓存
	err = cache.Clear(ctx)
	assert.NoError(t, err)

	// 验证缓存已清空
	size, err = cache.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 0, size, "清空后缓存大小应该是0")

	// 验证Redis中的数据也被清空
	exists, err := cache.Exists(ctx, "test1")
	assert.NoError(t, err)
	assert.False(t, exists, "清空后键不应该存在")
}

func TestHotKeyCache_Refresh(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 模拟数据源变化的加载器
	loadCount := 0
	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			loadCount++
			if loadCount == 1 {
				return map[int]string{
					1: "第一次加载",
				}, nil
			}
			return map[int]string{
				1: "第二次加载",
				2: "新增数据",
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_refresh", loader, config)
	defer cache.Stop()

	// 第一次获取数据
	value, exists, err := cache.Get(ctx, 1)
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "第一次加载", value)

	// 手动刷新
	err = cache.Refresh(ctx)
	assert.NoError(t, err)

	// 验证数据已更新
	value, exists, err = cache.Get(ctx, 1)
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "第二次加载", value)

	// 验证新增数据
	value, exists, err = cache.Get(ctx, 2)
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "新增数据", value)
}

func TestHotKeyCache_AutoRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过自动刷新测试（时间较长）")
	}

	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 模拟数据源变化（使用原子操作避免data race）
	var loadCount int64
	loader := &SQLDataLoader[string, int]{
		QueryFunc: func(ctx context.Context) (map[string]int, error) {
			count := atomic.AddInt64(&loadCount, 1)
			return map[string]int{
				"count": int(count),
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Second * 2, // 2秒刷新一次
		EnableAutoRefresh: true,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[string, int](client, "test_auto_refresh", loader, config)
	defer cache.Stop()

	// 第一次获取
	count, exists, err := cache.Get(ctx, "count")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, 1, count)

	// 等待自动刷新
	time.Sleep(time.Second * 3)

	// 再次获取，应该是更新后的值
	count, exists, err = cache.Get(ctx, "count")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Greater(t, count, 1, "自动刷新后计数应该增加")
}

func TestHotKeyCache_Stats(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{
				1: "test",
			}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_stats", loader, config)
	defer cache.Stop()

	// 加载数据
	_, err := cache.GetAll(ctx)
	assert.NoError(t, err)

	// 获取统计信息
	stats, err := cache.GetStats(ctx)
	assert.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, "test_stats", stats.KeyName)
	assert.Equal(t, 1, stats.LocalCacheSize)
	assert.NotZero(t, stats.LastRefreshTime, "最后刷新时间应该被设置")
	assert.Greater(t, stats.TTL, int64(0), "TTL应该大于0")
}

func TestHotKeyManager(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	manager := NewHotKeyManager(client, WithHotKeyTTL(time.Minute*5), WithHotKeyRefreshInterval(time.Minute), WithHotKeyAutoRefresh(false), WithHotKeyNamespace("test"))

	// 创建几个缓存
	loader1 := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{1: "cache1"}, nil
		},
	}

	loader2 := &SQLDataLoader[string, int]{
		QueryFunc: func(ctx context.Context) (map[string]int, error) {
			return map[string]int{"test": 2}, nil
		},
	}

	cache1 := NewHotKeyCache[int, string](client, "cache1", loader1, config)
	cache2 := NewHotKeyCache[string, int](client, "cache2", loader2, config)

	// 注册缓存到管理器
	manager.RegisterCache("cache1", cache1)
	manager.RegisterCache("cache2", cache2)

	// 测试获取缓存
	retrievedCache, exists := manager.GetCache("cache1")
	assert.True(t, exists, "应该能获取到已注册的缓存")
	assert.NotNil(t, retrievedCache)

	// 测试获取不存在的缓存
	_, exists = manager.GetCache("non_existent")
	assert.False(t, exists, "不应该能获取到不存在的缓存")

	// 测试刷新所有缓存
	err := manager.RefreshAll(ctx)
	assert.NoError(t, err)

	// 测试获取所有统计信息
	stats, err := manager.GetAllStats(ctx)
	assert.NoError(t, err)
	assert.Len(t, stats, 2, "应该有2个缓存的统计信息")

	// 清理
	manager.StopAll()
	cache1.Stop()
	cache2.Stop()
}

// TestHotKeyCache_CleanupExpired_FIFOEviction 测试 cleanupExpired 的 FIFO 驱逐逻辑
func TestHotKeyCache_CleanupExpired_FIFOEviction(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
		MaxLocalCacheSize: 3, // 小容量用于测试驱逐
	}

	cache := NewHotKeyCache[int, string](client, "test_cleanup_fifo", loader, config)
	// 立即停止自动启动的 cleanup goroutine（使用 1 分钟 ticker，太慢）
	cache.Stop()

	// 重置 once 和 stopChan，使用快速 ticker 手动重启 cleanup goroutine
	cache.once = sync.Once{}
	cache.stopChan = make(chan struct{})
	cache.cleanupTicker = time.NewTicker(10 * time.Millisecond)
	go cache.cleanupExpired()

	// 添加超过 MaxLocalCacheSize 的条目
	for i := 1; i <= 10; i++ {
		cache.Set(ctx, i, fmt.Sprintf("value_%d", i))
	}

	// 等待清理触发
	time.Sleep(100 * time.Millisecond)

	cache.Stop()

	// 验证本地缓存已被驱逐到不超过 MaxLocalCacheSize
	cache.mu.RLock()
	size := len(cache.localCache)
	cache.mu.RUnlock()
	assert.LessOrEqual(t, size, config.MaxLocalCacheSize, "清理后缓存大小应不超过 MaxLocalCacheSize")
}

// TestHotKeyCache_CleanupExpired_StaleEntries 测试 cleanupExpired 跳过陈旧条目（已 Delete 但仍在 accessOrder 中）
func TestHotKeyCache_CleanupExpired_StaleEntries(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
		MaxLocalCacheSize: 2, // 极小容量
	}

	cache := NewHotKeyCache[int, string](client, "test_cleanup_stale", loader, config)
	// 立即停止自动启动的 cleanup goroutine
	cache.Stop()

	// 重置并使用快速 ticker 手动重启
	cache.once = sync.Once{}
	cache.stopChan = make(chan struct{})
	cache.cleanupTicker = time.NewTicker(10 * time.Millisecond)
	go cache.cleanupExpired()

	// 添加条目（5 个，超过 MaxLocalCacheSize=2）
	for i := 1; i <= 5; i++ {
		cache.Set(ctx, i, fmt.Sprintf("val_%d", i))
	}

	// 删除部分键（从 localCache 删除，但 accessOrder 中仍保留旧条目）
	// 删除后 localCache 有 3 个条目（3,4,5），仍 > MaxLocalCacheSize=2
	cache.Delete(ctx, 1)
	cache.Delete(ctx, 2)

	// 等待清理触发，陈旧条目应被跳过并从 accessOrder 移除
	time.Sleep(150 * time.Millisecond)

	cache.Stop()

	// 验证 accessOrder 已被重建（不包含陈旧条目）
	cache.mu.RLock()
	orderLen := len(cache.accessOrder)
	cache.mu.RUnlock()
	assert.LessOrEqual(t, orderLen, config.MaxLocalCacheSize, "accessOrder 应已清理陈旧条目")
}

// TestHotKeyCache_Get_LoadAllError 测试 Get 在 LoadAll 失败时返回错误
func TestHotKeyCache_Get_LoadAllError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return nil, fmt.Errorf("load error")
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_get_err", loader, config)
	defer cache.Stop()

	_, _, err := cache.Get(ctx, 1)
	assert.Error(t, err, "Get 应返回 LoadAll 的错误")
}

// TestHotKeyCache_LoadAll_UnmarshalError 测试 LoadAll 在 JSON 反序列化失败时回退到数据源
func TestHotKeyCache_LoadAll_UnmarshalError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 写入损坏的 JSON 数据到 Redis
	redisKey := "test:test_loadall_unmarshal"
	client.Set(ctx, redisKey, "invalid_json_data", time.Minute)

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{1: "fallback"}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_loadall_unmarshal", loader, config)
	defer cache.Stop()

	// LoadAll 应回退到数据源
	data, err := cache.LoadAll(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "fallback", data[1])
}

// TestHotKeyCache_SaveToRedis_MarshalError 测试 SaveToRedis 在序列化失败时返回错误
func TestHotKeyCache_SaveToRedis_MarshalError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[string, chan int]{
		QueryFunc: func(ctx context.Context) (map[string]chan int, error) {
			return map[string]chan int{}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[string, chan int](client, "test_marshal_err", loader, config)
	defer cache.Stop()

	// 设置一个无法 JSON 序列化的值（channel 不能被 json.Marshal）
	err := cache.Set(ctx, "bad", make(chan int))
	assert.Error(t, err, "序列化失败应返回错误")
}

// TestHotKeyCache_Refresh_Error 测试 Refresh 在 loader 失败时返回错误
func TestHotKeyCache_Refresh_Error(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return nil, fmt.Errorf("refresh error")
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_refresh_err", loader, config)
	defer cache.Stop()

	err := cache.Refresh(ctx)
	assert.Error(t, err, "Refresh 应返回 loader 的错误")
}

// TestHotKeyCache_Exists_GetAllError 测试 Exists 在 GetAll 失败时返回错误
func TestHotKeyCache_Exists_GetAllError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return nil, fmt.Errorf("exists error")
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_exists_err", loader, config)
	defer cache.Stop()

	_, err := cache.Exists(ctx, 1)
	assert.Error(t, err, "Exists 应返回 GetAll 的错误")
}

// TestHotKeyCache_Keys_GetAllError 测试 Keys 在 GetAll 失败时返回错误
func TestHotKeyCache_Keys_GetAllError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return nil, fmt.Errorf("keys error")
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_keys_err", loader, config)
	defer cache.Stop()

	_, err := cache.Keys(ctx)
	assert.Error(t, err, "Keys 应返回 GetAll 的错误")
}

// TestHotKeyCache_Size_GetAllError 测试 Size 在 GetAll 失败时返回错误
func TestHotKeyCache_Size_GetAllError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return nil, fmt.Errorf("size error")
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_size_err", loader, config)
	defer cache.Stop()

	_, err := cache.Size(ctx)
	assert.Error(t, err, "Size 应返回 GetAll 的错误")
}

// TestHotKeyCache_GetStats_TTLError 测试 GetStats 在 TTL 查询失败时仍返回结果
func TestHotKeyCache_GetStats_TTLError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{1: "test"}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_stats_ttl_err", loader, config)
	defer cache.Stop()

	// 先加载数据
	cache.GetAll(ctx)

	// 关闭客户端使 TTL 查询失败
	client.Close()

	// GetStats 应仍返回结果（TTL=0）
	stats, err := cache.GetStats(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, int64(0), stats.TTL, "TTL 查询失败时应返回 0")
}

// TestHotKeyManager_RegisterAndGet 测试 HotKeyManager 的 Register 和 Get 方法
func TestHotKeyManager_RegisterAndGet(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	manager := NewHotKeyManager(client, WithHotKeyTTL(time.Minute*5), WithHotKeyRefreshInterval(time.Minute), WithHotKeyAutoRefresh(false), WithHotKeyNamespace("test"))

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{1: "data"}, nil
		},
	}

	cache := NewHotKeyCache[int, string](client, "mgr_cache", loader, config)
	defer cache.Stop()

	// 测试 Register
	manager.Register("my_cache", cache)

	// 测试 Get - 存在
	retrieved, exists := manager.Get("my_cache")
	assert.True(t, exists, "应能获取到已注册的缓存")
	assert.NotNil(t, retrieved)

	// 测试 Get - 不存在
	_, exists = manager.Get("non_existent")
	assert.False(t, exists, "不应获取到不存在的缓存")
}

// TestHotKeyManager_RefreshAll_Error 测试 RefreshAll 在某个缓存刷新失败时返回错误
func TestHotKeyManager_RefreshAll_Error(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	manager := NewHotKeyManager(client, WithHotKeyTTL(time.Minute*5), WithHotKeyRefreshInterval(time.Minute), WithHotKeyAutoRefresh(false), WithHotKeyNamespace("test"))

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return nil, fmt.Errorf("refresh all error")
		},
	}

	cache := NewHotKeyCache[int, string](client, "mgr_refresh_err", loader, config)
	defer cache.Stop()

	manager.RegisterCache("err_cache", cache)

	err := manager.RefreshAll(ctx)
	assert.Error(t, err, "RefreshAll 应在某个缓存刷新失败时返回错误")
}

// TestHotKeyCache_NewHotKeyCache_AutoRefreshEnabled 测试创建启用自动刷新的缓存
func TestHotKeyCache_NewHotKeyCache_AutoRefreshEnabled(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{1: "data"}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: true,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_auto_enabled", loader, config)
	defer cache.Stop()

	assert.NotNil(t, cache)
	// 验证自动刷新 goroutine 已启动
	assert.Equal(t, true, config.EnableAutoRefresh)
}

// TestHotKeyCache_Set_ExistingKey 测试 Set 对已存在的键不重复追加 accessOrder
func TestHotKeyCache_Set_ExistingKey(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return map[int]string{}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_set_existing", loader, config)
	defer cache.Stop()

	// 设置键
	cache.Set(ctx, 1, "first")
	cache.Set(ctx, 1, "second") // 重复设置同一键

	// accessOrder 应只有一个条目
	cache.mu.RLock()
	orderLen := len(cache.accessOrder)
	cache.mu.RUnlock()
	assert.Equal(t, 1, orderLen, "重复设置同一键不应追加 accessOrder")
}

// TestHotKeyCache_AutoRefresh_RefreshError 测试 autoRefresh 中 Refresh 失败时记录日志但不停止
func TestHotKeyCache_AutoRefresh_RefreshError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	var loadCount int64
	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			count := atomic.AddInt64(&loadCount, 1)
			// 第一次加载成功，后续自动刷新失败
			if count > 1 {
				return nil, fmt.Errorf("auto refresh error")
			}
			return map[int]string{1: "first"}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Millisecond * 200, // 快速刷新以便测试
		EnableAutoRefresh: true,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_autorefresh_refresh_err", loader, config)
	defer cache.Stop()

	// 等待自动刷新触发并失败（至少两次加载：初始 + 至少一次自动刷新）
	time.Sleep(time.Millisecond * 600)

	// loadCount 应大于 1，说明自动刷新触发过 Refresh
	assert.Greater(t, atomic.LoadInt64(&loadCount), int64(1), "autoRefresh 应触发多次 Refresh")
}

// TestHotKeyCache_NewHotKeyCache_AutoRefreshPanic 测试 autoRefresh 中 loader panic 时 OnPanic 捕获
func TestHotKeyCache_NewHotKeyCache_AutoRefreshPanic(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	var loadCount int64
	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			count := atomic.AddInt64(&loadCount, 1)
			if count > 1 {
				panic("loader panic in autoRefresh")
			}
			return map[int]string{1: "first"}, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Millisecond * 200, // 快速刷新以便测试
		EnableAutoRefresh: true,
		Namespace:         "test",
	}

	// 不应 panic（OnPanic 应捕获 autoRefresh 中的 panic）
	cache := NewHotKeyCache[int, string](client, "test_autorefresh_panic", loader, config)
	defer cache.Stop()

	// 等待自动刷新触发 panic
	time.Sleep(time.Millisecond * 600)

	// 验证 loadCount 大于 1（说明 autoRefresh 触发过）
	assert.Greater(t, atomic.LoadInt64(&loadCount), int64(1), "autoRefresh 应触发过导致 panic")
}

// 基准测试
func BenchmarkHotKeyCache_Get(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			data := make(map[int]string)
			for i := 1; i <= 1000; i++ {
				data[i] = fmt.Sprintf("value_%d", i)
			}
			return data, nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "bench",
	}

	cache := NewHotKeyCache[int, string](client, "benchmark", loader, config)
	defer cache.Stop()

	// 预热缓存
	cache.GetAll(context.Background())

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 1
		for pb.Next() {
			cache.Get(context.Background(), i%1000+1)
			i++
		}
	})
}

func BenchmarkHotKeyCache_Set(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	loader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			return make(map[int]string), nil
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Minute,
		EnableAutoRefresh: false,
		Namespace:         "bench",
	}

	cache := NewHotKeyCache[int, string](client, "benchmark_set", loader, config)
	defer cache.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 1
		for pb.Next() {
			cache.Set(context.Background(), i, fmt.Sprintf("value_%d", i))
			i++
		}
	})
}

// TestNewHotKeyCache_AutoRefreshOnPanic 测试 NewHotKeyCache 中 autoRefresh 的 OnPanic 回调
// 通过使用一个会 panic 的 loader 并启用 autoRefresh 来触发 OnPanic
func TestNewHotKeyCache_AutoRefreshOnPanic(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	// 创建一个会 panic 的 loader（仅在 autoRefresh 调用时 panic）
	panickingLoader := &SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			panic("simulated panic in loader")
		},
	}

	config := HotKeyConfig{
		DefaultTTL:        time.Minute * 5,
		RefreshInterval:   time.Millisecond * 50, // 快速刷新以尽快触发 panic
		EnableAutoRefresh: true,
		Namespace:         "test",
	}

	cache := NewHotKeyCache[int, string](client, "test_panic_autorefresh", panickingLoader, config)
	defer cache.Stop()

	// 等待 autoRefresh 触发 panic 并被 OnPanic 捕获
	// 如果 OnPanic 未生效，测试会因 panic 而失败
	time.Sleep(time.Millisecond * 200)
}
