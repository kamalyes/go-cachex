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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
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

	manager := NewHotKeyManager(client, config)

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
