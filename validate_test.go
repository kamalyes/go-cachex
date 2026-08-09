/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-08-09 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-08-09 00:00:00
 * @FilePath: \go-cachex\validate_test.go
 * @Description: 缓存操作通用验证函数测试，覆盖所有分支
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateKey(t *testing.T) {
	// nil key 无效
	assert.ErrorIs(t, ValidateKey(nil), ErrInvalidKey)
	// 有效 key
	assert.NoError(t, ValidateKey([]byte("valid_key")))
	// 空切片非 nil，视为有效
	assert.NoError(t, ValidateKey([]byte{}))
}

func TestValidateValue(t *testing.T) {
	// nil value 无效
	assert.ErrorIs(t, ValidateValue(nil), ErrInvalidValue)
	// 有效 value
	assert.NoError(t, ValidateValue([]byte("valid_value")))
	// 空切片非 nil，视为有效
	assert.NoError(t, ValidateValue([]byte{}))
}

func TestValidateTTL(t *testing.T) {
	// -1 表示永不过期，有效
	assert.NoError(t, ValidateTTL(-1))
	// 0 表示立即过期，有效
	assert.NoError(t, ValidateTTL(0))
	// 正数 TTL 有效
	assert.NoError(t, ValidateTTL(time.Second))
	// 小于 -1 无效
	assert.ErrorIs(t, ValidateTTL(-2*time.Second), ErrInvalidTTL)
	assert.ErrorIs(t, ValidateTTL(-1*time.Nanosecond-time.Second), ErrInvalidTTL)
}

func TestValidateInitialized(t *testing.T) {
	// 已初始化
	assert.NoError(t, ValidateInitialized(true))
	// 未初始化
	assert.ErrorIs(t, ValidateInitialized(false), ErrNotInitialized)
}

func TestValidateClosed(t *testing.T) {
	// 未关闭
	assert.NoError(t, ValidateClosed(false))
	// 已关闭
	assert.ErrorIs(t, ValidateClosed(true), ErrClosed)
}

func TestValidateCapacity(t *testing.T) {
	// max>0 且 current>=max，超出容量
	assert.ErrorIs(t, ValidateCapacity(10, 10), ErrCapacityExceeded)
	assert.ErrorIs(t, ValidateCapacity(15, 10), ErrCapacityExceeded)
	// max>0 且 current<max，未超出
	assert.NoError(t, ValidateCapacity(5, 10))
	assert.NoError(t, ValidateCapacity(0, 10))
	// max<=0 不检查容量
	assert.NoError(t, ValidateCapacity(100, 0))
	assert.NoError(t, ValidateCapacity(100, -1))
}

func TestValidateBasicOp(t *testing.T) {
	// nil key 优先返回 ErrInvalidKey
	assert.ErrorIs(t, ValidateBasicOp(nil, true, false), ErrInvalidKey)
	// 未初始化返回 ErrNotInitialized
	assert.ErrorIs(t, ValidateBasicOp([]byte("k"), false, false), ErrNotInitialized)
	// 已关闭返回 ErrClosed
	assert.ErrorIs(t, ValidateBasicOp([]byte("k"), true, true), ErrClosed)
	// 全部有效返回 nil
	assert.NoError(t, ValidateBasicOp([]byte("k"), true, false))
}

func TestValidateWriteOp(t *testing.T) {
	// nil key 优先返回 ErrInvalidKey
	assert.ErrorIs(t, ValidateWriteOp(nil, []byte("v"), true, false), ErrInvalidKey)
	// 未初始化返回 ErrNotInitialized
	assert.ErrorIs(t, ValidateWriteOp([]byte("k"), []byte("v"), false, false), ErrNotInitialized)
	// 已关闭返回 ErrClosed
	assert.ErrorIs(t, ValidateWriteOp([]byte("k"), []byte("v"), true, true), ErrClosed)
	// nil value 返回 ErrInvalidValue
	assert.ErrorIs(t, ValidateWriteOp([]byte("k"), nil, true, false), ErrInvalidValue)
	// 全部有效返回 nil
	assert.NoError(t, ValidateWriteOp([]byte("k"), []byte("v"), true, false))
}

func TestValidateWriteWithTTLOp(t *testing.T) {
	// nil key 优先返回 ErrInvalidKey
	assert.ErrorIs(t, ValidateWriteWithTTLOp(nil, []byte("v"), time.Second, true, false), ErrInvalidKey)
	// nil value 返回 ErrInvalidValue
	assert.ErrorIs(t, ValidateWriteWithTTLOp([]byte("k"), nil, time.Second, true, false), ErrInvalidValue)
	// 无效 TTL 返回 ErrInvalidTTL
	assert.ErrorIs(t, ValidateWriteWithTTLOp([]byte("k"), []byte("v"), -2*time.Second, true, false), ErrInvalidTTL)
	// 全部有效（带正数 TTL）返回 nil
	assert.NoError(t, ValidateWriteWithTTLOp([]byte("k"), []byte("v"), time.Second, true, false))
	// 全部有效（TTL=-1 永不过期）返回 nil
	assert.NoError(t, ValidateWriteWithTTLOp([]byte("k"), []byte("v"), -1, true, false))
	// 全部有效（TTL=0 立即过期）返回 nil
	assert.NoError(t, ValidateWriteWithTTLOp([]byte("k"), []byte("v"), 0, true, false))
}
