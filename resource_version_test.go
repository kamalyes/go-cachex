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
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
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
