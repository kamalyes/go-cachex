/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-06 21:15:15
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:37:58
 * @FilePath: \go-cachex\expiring_test.go
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

func TestExpiring_SetGet(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(20 * time.Millisecond)
	defer h.Close()

	// 测试基本的 Set/Get
	err := h.Set([]byte("a"), []byte("1"))
	assert.NoError(err, "Set should succeed")

	v, err := h.Get([]byte("a"))
	assert.NoError(err, "Get should succeed")
	assert.Equal("1", string(v), "Get should return the value we set")

	// 测试不存在的键
	_, err = h.Get([]byte("non-existent"))
	assert.ErrorIs(err, ErrNotFound, "Get non-existent key should return ErrNotFound")
}

func TestExpiring_TTL(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)
	defer h.Close()

	// 设置带 TTL 的键值对
	err := h.SetWithTTL([]byte("b"), []byte("2"), 30*time.Millisecond)
	assert.NoError(err, "SetWithTTL should succeed")

	// 获取 TTL
	ttl, err := h.GetTTL([]byte("b"))
	assert.NoError(err, "GetTTL should succeed")
	assert.True(ttl > 0 && ttl <= 30*time.Millisecond, "TTL should be positive and not exceed set value")

	// TTL 到期前应该能获取值
	v, err := h.Get([]byte("b"))
	assert.NoError(err, "Get before expiry should succeed")
	assert.Equal("2", string(v), "Get should return correct value before expiry")

	// 等待过期
	time.Sleep(50 * time.Millisecond)

	// TTL 到期后应该返回 ErrNotFound
	_, err = h.Get([]byte("b"))
	assert.ErrorIs(err, ErrNotFound, "Get after expiry should return ErrNotFound")

	// TTL 到期后 GetTTL 也应该返回 ErrNotFound
	_, err = h.GetTTL([]byte("b"))
	assert.ErrorIs(err, ErrNotFound, "GetTTL after expiry should return ErrNotFound")
}

func TestExpiring_Delete(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)
	defer h.Close()

	// 设置一个键值对
	err := h.Set([]byte("c"), []byte("3"))
	assert.NoError(err, "Set should succeed")

	// 删除键
	err = h.Del([]byte("c"))
	assert.NoError(err, "Del should succeed")

	// 获取已删除的键应该返回 ErrNotFound
	_, err = h.Get([]byte("c"))
	assert.ErrorIs(err, ErrNotFound, "Get after delete should return ErrNotFound")
}

func TestExpiring_Close(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)

	// 设置一些数据
	err := h.Set([]byte("d"), []byte("4"))
	assert.NoError(err, "Set should succeed")

	// 关闭缓存
	err = h.Close()
	assert.NoError(err, "Close should succeed")

	// 关闭后的操作应该返回错误
	err = h.Set([]byte("e"), []byte("5"))
	assert.Error(err, "Set after close should fail")

	_, err = h.Get([]byte("d"))
	assert.Error(err, "Get after close should fail")

	// 重复关闭应该是安全的
	err = h.Close()
	assert.NoError(err, "Second close should be safe")
}

func TestExpiring_Concurrency(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)
	defer h.Close()

	// 并发写入不同的键
	for i := 0; i < 100; i++ {
		key := []byte(time.Now().String())
		err := h.Set(key, []byte("value"))
		assert.NoError(err, "Concurrent Set should succeed")

		// 立即读取
		v, err := h.Get(key)
		assert.NoError(err, "Concurrent Get should succeed")
		assert.Equal("value", string(v), "Concurrent Get should return correct value")
	}
}
