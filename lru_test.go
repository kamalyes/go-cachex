/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 22:25:50
 * @FilePath: \go-cachex\lru_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLRU_SetGet(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(2)
	defer h.Close()

	// 设置两个键值对
	err := h.Set([]byte("k1"), []byte("v1"))
	assert.NoError(err, "First Set should succeed")

	err = h.Set([]byte("k2"), []byte("v2"))
	assert.NoError(err, "Second Set should succeed")

	// 验证键存在且值正确
	v, err := h.Get([]byte("k1"))
	assert.NoError(err, "Get k1 should succeed")
	assert.Equal("v1", string(v), "k1 should have correct value")

	// 检查 k1 的访问是否更新了其位置（使其更新）
	v, err = h.Get([]byte("k1"))
	assert.NoError(err, "Second Get k1 should succeed")
	assert.Equal("v1", string(v), "k1 should still have correct value")

	// 添加第三个键，由于 k1 最近被访问过，应该驱逐 k2
	err = h.Set([]byte("k3"), []byte("v3"))
	assert.NoError(err, "Third Set should succeed")

	// 验证 k2 已被驱逐
	_, err = h.Get([]byte("k2"))
	assert.ErrorIs(err, ErrNotFound, "k2 should be evicted")

	// 验证 k1 和 k3 仍然存在
	v, err = h.Get([]byte("k1"))
	assert.NoError(err, "k1 should still exist")
	assert.Equal("v1", string(v), "k1 should have correct value")

	v, err = h.Get([]byte("k3"))
	assert.NoError(err, "k3 should exist")
	assert.Equal("v3", string(v), "k3 should have correct value")
}

func TestLRU_TTL(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(10)
	defer h.Close()

	// 设置带 TTL 的键值对
	err := h.SetWithTTL([]byte("kt"), []byte("vt"), 50*time.Millisecond)
	assert.NoError(err, "SetWithTTL should succeed")

	// 验证 TTL
	ttl, err := h.GetTTL([]byte("kt"))
	assert.NoError(err, "GetTTL should succeed")
	assert.True(ttl > 0 && ttl <= 50*time.Millisecond, "TTL should be positive and not exceed set value")

	// TTL 到期前应该能获取值
	v, err := h.Get([]byte("kt"))
	assert.NoError(err, "Get before expiry should succeed")
	assert.Equal("vt", string(v), "Should get correct value before expiry")

	// 等待过期
	time.Sleep(80 * time.Millisecond)

	// 验证过期后无法获取
	_, err = h.Get([]byte("kt"))
	assert.ErrorIs(err, ErrNotFound, "Key should be expired")

	// 验证过期后 TTL 也返回错误
	_, err = h.GetTTL([]byte("kt"))
	assert.ErrorIs(err, ErrNotFound, "GetTTL after expiry should return ErrNotFound")
}

func TestLRU_Delete(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(10)
	defer h.Close()

	// 设置一些键值对
	err := h.Set([]byte("k1"), []byte("v1"))
	assert.NoError(err, "Set should succeed")

	// 删除键
	err = h.Del([]byte("k1"))
	assert.NoError(err, "Del should succeed")

	// 验证键已被删除
	_, err = h.Get([]byte("k1"))
	assert.ErrorIs(err, ErrNotFound, "Key should be deleted")

	// 删除不存在的键也应该成功
	err = h.Del([]byte("non-existent"))
	assert.NoError(err, "Del non-existent key should succeed")
}

func TestLRU_Capacity(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(3)
	defer h.Close()

	// 添加到容量上限
	for i := 0; i < 3; i++ {
		key := []byte(string(rune('a' + i)))
		err := h.Set(key, []byte("value"))
		assert.NoError(err, "Set within capacity should succeed")
	}

	// 所有键都应该存在
	for i := 0; i < 3; i++ {
		key := []byte(string(rune('a' + i)))
		_, err := h.Get(key)
		assert.NoError(err, "All keys within capacity should exist")
	}

	// 添加第四个键（应该驱逐最老的键 'a'）
	err := h.Set([]byte("d"), []byte("value"))
	assert.NoError(err, "Set beyond capacity should succeed with eviction")

	// 最早的键应该被驱逐
	_, err = h.Get([]byte("a"))
	assert.ErrorIs(err, ErrNotFound, "Oldest key should be evicted")
}
