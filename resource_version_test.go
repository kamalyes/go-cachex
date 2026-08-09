/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-06-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-06-30 11:06:26
 * @FilePath: \go-cachex\resource_version_test.go
 * @Description: 资源版本追踪器测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionTracker_UpdateAndGetVersion(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	err := tracker.UpdateVersion(ctx, "resource1", 100)
	assert.NoError(t, err)

	version, err := tracker.GetVersion(ctx, "resource1")
	assert.NoError(t, err)
	assert.Equal(t, int64(100), version)
}

func TestVersionTracker_HasChanged(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()

	changed, version := tracker.HasChanged(ctx, "resource1", 0)
	assert.True(t, changed)
	assert.Equal(t, int64(0), version)

	err := tracker.UpdateVersion(ctx, "resource1", 200)
	assert.NoError(t, err)

	changed, version = tracker.HasChanged(ctx, "resource1", 0)
	assert.True(t, changed)
	assert.Equal(t, int64(200), version)

	changed, version = tracker.HasChanged(ctx, "resource1", 200)
	assert.False(t, changed)
	assert.Equal(t, int64(200), version)
}

func TestVersionTracker_Touch(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	err := tracker.Touch(ctx, "resource1")
	assert.NoError(t, err)

	version, err := tracker.GetVersion(ctx, "resource1")
	assert.NoError(t, err)
	assert.Greater(t, version, int64(0))

	time.Sleep(1 * time.Millisecond)

	err = tracker.Touch(ctx, "resource1")
	assert.NoError(t, err)

	newVersion, err := tracker.GetVersion(ctx, "resource1")
	assert.NoError(t, err)
	assert.Greater(t, newVersion, version)
}

func TestVersionTracker_BatchUpdate(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	err := tracker.BatchUpdateVersion(ctx, []string{"r1", "r2", "r3"}, 500)
	assert.NoError(t, err)

	v1, _ := tracker.GetVersion(ctx, "r1")
	v2, _ := tracker.GetVersion(ctx, "r2")
	v3, _ := tracker.GetVersion(ctx, "r3")

	assert.Equal(t, int64(500), v1)
	assert.Equal(t, int64(500), v2)
	assert.Equal(t, int64(500), v3)
}

func TestVersionTracker_GetOrCreateVersion(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	version, err := tracker.GetOrCreateVersion(ctx, "new_resource")
	assert.NoError(t, err)
	assert.Greater(t, version, int64(0))

	sameVersion, err := tracker.GetOrCreateVersion(ctx, "new_resource")
	assert.NoError(t, err)
	assert.Equal(t, version, sameVersion)
}

func TestVersionTracker_DeleteVersion(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	err := tracker.UpdateVersion(ctx, "resource1", 100)
	assert.NoError(t, err)

	err = tracker.DeleteVersion(ctx, "resource1")
	assert.NoError(t, err)

	_, err = tracker.GetVersion(ctx, "resource1")
	assert.Error(t, err)
	assert.Equal(t, ErrNotFound, err)
}

func TestVersionTracker_NilRedis(t *testing.T) {
	tracker := NewVersionTracker(nil, "test:")

	ctx := context.Background()

	err := tracker.UpdateVersion(ctx, "resource1", 100)
	assert.NoError(t, err)

	changed, version := tracker.HasChanged(ctx, "resource1", 0)
	assert.True(t, changed)
	assert.Equal(t, int64(0), version)

	version, err = tracker.GetVersion(ctx, "resource1")
	assert.Error(t, err)
	assert.Equal(t, ErrUnavailable, err)
}

func TestVersionTracker_GetVersionKey(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "prefix:")
	key := tracker.GetVersionKey("resource1")
	assert.Equal(t, "prefix:resource1", key)

	tracker2 := NewVersionTracker(client, "")
	key2 := tracker2.GetVersionKey("resource1")
	assert.Equal(t, "cachex:version:resource1", key2)
}

// failingSetRedisClient 包装真实 *redis.Client，但 Set 始终返回指定错误
// 用于测试 UpdateVersion 失败分支（Get 可正常返回 redis.Nil 表示 key 不存在）
type failingSetRedisClient struct {
	*redis.Client
	setErr error
}

func (c *failingSetRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	return redis.NewStatusResult("", c.setErr)
}

// TestVersionTracker_BatchUpdateVersionWithNow 验证批量使用当前时间戳更新版本号
func TestVersionTracker_BatchUpdateVersionWithNow(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	before := nowTimestamp()
	time.Sleep(time.Millisecond) // 确保时间戳递增

	err := tracker.BatchUpdateVersionWithNow(ctx, []string{"r1", "r2", "r3"})
	assert.NoError(t, err)

	for _, id := range []string{"r1", "r2", "r3"} {
		v, err := tracker.GetVersion(ctx, id)
		assert.NoError(t, err)
		assert.Greater(t, v, before, "版本号应为当前时间戳，大于 before")
	}
}

// TestVersionTracker_TouchAll 验证批量 Touch 多个资源版本号
func TestVersionTracker_TouchAll(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	// 先用旧版本号初始化
	err := tracker.BatchUpdateVersion(ctx, []string{"t1", "t2"}, 100)
	require.NoError(t, err)

	time.Sleep(time.Millisecond)
	err = tracker.TouchAll(ctx, []string{"t1", "t2", "t3"})
	assert.NoError(t, err)

	for _, id := range []string{"t1", "t2", "t3"} {
		v, err := tracker.GetVersion(ctx, id)
		assert.NoError(t, err)
		assert.Greater(t, v, int64(100), "TouchAll 后版本号应更新为当前时间戳")
	}
}

// TestVersionTracker_DeleteVersions 验证批量删除资源版本记录
func TestVersionTracker_DeleteVersions(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	// 预置数据
	err := tracker.BatchUpdateVersion(ctx, []string{"d1", "d2", "d3"}, 999)
	require.NoError(t, err)

	// 批量删除
	err = tracker.DeleteVersions(ctx, []string{"d1", "d2"})
	assert.NoError(t, err)

	// d1/d2 应不存在，d3 仍在
	_, err = tracker.GetVersion(ctx, "d1")
	assert.Equal(t, ErrNotFound, err)
	_, err = tracker.GetVersion(ctx, "d2")
	assert.Equal(t, ErrNotFound, err)
	v, err := tracker.GetVersion(ctx, "d3")
	assert.NoError(t, err)
	assert.Equal(t, int64(999), v)
}

// TestVersionTracker_GetVersionParseError 验证 GetVersion 在值非整数时返回解析错误
func TestVersionTracker_GetVersionParseError(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	// 直接写入非数字值，触发 ParseInt 错误分支
	require.NoError(t, client.Set(ctx, tracker.GetVersionKey("bad"), "not-a-number", 0).Err())

	_, err := tracker.GetVersion(ctx, "bad")
	assert.Error(t, err)
	assert.NotEqual(t, ErrNotFound, err)
}

// TestVersionTracker_HasChangedNonNotFoundError 验证 HasChanged 在 GetVersion 返回非 ErrNotFound 错误时的分支
func TestVersionTracker_HasChangedNonNotFoundError(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	// 写入非数字值，GetVersion 返回 ParseInt 错误（非 ErrNotFound）
	require.NoError(t, client.Set(ctx, tracker.GetVersionKey("bad"), "not-a-number", 0).Err())

	changed, version := tracker.HasChanged(ctx, "bad", 123)
	assert.True(t, changed, "GetVersion 返回非 ErrNotFound 错误时应判定为已变化")
	assert.Equal(t, int64(0), version, "错误时 version 应为 0")
}

// TestVersionTracker_NilRedisAllPaths 验证所有方法在 nil redis 客户端时的短路返回
func TestVersionTracker_NilRedisAllPaths(t *testing.T) {
	tracker := NewVersionTracker(nil, "test:")
	ctx := context.Background()

	// UpdateVersion -> nil 客户端直接返回 nil
	assert.NoError(t, tracker.UpdateVersion(ctx, "r1", 1))
	// UpdateVersionWithNow -> 委托 UpdateVersion
	assert.NoError(t, tracker.UpdateVersionWithNow(ctx, "r1"))
	// BatchUpdateVersion -> nil 客户端直接返回 nil
	assert.NoError(t, tracker.BatchUpdateVersion(ctx, []string{"r1", "r2"}, 1))
	// BatchUpdateVersionWithNow -> 委托 BatchUpdateVersion
	assert.NoError(t, tracker.BatchUpdateVersionWithNow(ctx, []string{"r1", "r2"}))
	// DeleteVersion -> nil 客户端直接返回 nil
	assert.NoError(t, tracker.DeleteVersion(ctx, "r1"))
	// DeleteVersions -> nil 客户端直接返回 nil
	assert.NoError(t, tracker.DeleteVersions(ctx, []string{"r1", "r2"}))
	// TouchAll -> 委托 BatchUpdateVersionWithNow
	assert.NoError(t, tracker.TouchAll(ctx, []string{"r1", "r2"}))
}

// TestVersionTracker_GetOrCreateVersionNilRedis 验证 nil redis 时 GetOrCreateVersion 返回当前时间戳
func TestVersionTracker_GetOrCreateVersionNilRedis(t *testing.T) {
	tracker := NewVersionTracker(nil, "test:")
	ctx := context.Background()

	before := nowTimestamp()
	time.Sleep(time.Millisecond) // 确保时间戳递增
	v, err := tracker.GetOrCreateVersion(ctx, "r1")
	assert.NoError(t, err)
	assert.Greater(t, v, before, "nil redis 时应返回当前时间戳")
}

// TestVersionTracker_GetOrCreateVersionSetError 验证 GetOrCreateVersion 在 UpdateVersion 失败时返回错误
func TestVersionTracker_GetOrCreateVersionSetError(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	// 使用包装客户端：Get 委托给真实 client（key 不存在 -> redis.Nil -> ErrNotFound），
	// Set 始终返回错误，触发 GetOrCreateVersion 的 setErr 分支
	mockErr := errors.New("set failed")
	mockClient := &failingSetRedisClient{Client: client, setErr: mockErr}
	tracker := NewVersionTracker(mockClient, "test:")

	ctx := context.Background()
	_, err := tracker.GetOrCreateVersion(ctx, "nonexistent-key")
	assert.ErrorIs(t, err, mockErr, "UpdateVersion 失败时应返回该错误")
}

// TestVersionTracker_GetOrCreateVersionGenericError 验证 GetOrCreateVersion 在 GetVersion 返回非 ErrNotFound 错误时返回错误
func TestVersionTracker_GetOrCreateVersionGenericError(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	// 写入非数字值，GetVersion 返回 ParseInt 错误（非 ErrNotFound），触发 return 0, err 分支
	require.NoError(t, client.Set(ctx, tracker.GetVersionKey("bad"), "not-a-number", 0).Err())

	_, err := tracker.GetOrCreateVersion(ctx, "bad")
	assert.Error(t, err)
	assert.NotEqual(t, ErrNotFound, err)
}

// TestVersionTracker_GetVersionRedisError 验证 GetVersion 在 Redis 返回非 Nil 错误时的分支
func TestVersionTracker_GetVersionRedisError(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	tracker := NewVersionTracker(client, "test:")

	ctx := context.Background()
	// 关闭 miniredis 后，Get 返回连接错误（非 redis.Nil），触发 return 0, err 分支
	s.Close()
	_, err := tracker.GetVersion(ctx, "any")
	assert.Error(t, err)
	assert.NotEqual(t, ErrNotFound, err)
}
