/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-06 22:35:20
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 22:35:20
 * @FilePath: \go-cachex\validate.go
 * @Description: 缓存操作的通用验证
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import "time"

// ValidateKey 验证键是否有效
func ValidateKey(key []byte) error {
	if key == nil {
		return ErrInvalidKey
	}
	return nil
}

// ValidateValue 验证值是否有效
func ValidateValue(value []byte) error {
	if value == nil {
		return ErrInvalidValue
	}
	return nil
}

// ValidateTTL 验证 TTL 是否有效（允许 -1 表示无限期）
func ValidateTTL(ttl time.Duration) error {
	if ttl < -1 {
		return ErrInvalidTTL
	}
	return nil
}

// ValidateInitialized 验证缓存是否已初始化
func ValidateInitialized(initialized bool) error {
	if !initialized {
		return ErrNotInitialized
	}
	return nil
}

// ValidateClosed 验证缓存是否已关闭
func ValidateClosed(closed bool) error {
	if closed {
		return ErrClosed
	}
	return nil
}

// ValidateCapacity 验证是否超出容量限制
func ValidateCapacity(current, max int) error {
	if max > 0 && current >= max {
		return ErrCapacityExceeded
	}
	return nil
}

// ValidateBasicOp 验证基本操作的参数（检查键、缓存状态）
func ValidateBasicOp(key []byte, initialized, closed bool) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ValidateInitialized(initialized); err != nil {
		return err
	}
	if err := ValidateClosed(closed); err != nil {
		return err
	}
	return nil
}

// ValidateWriteOp 验证写操作的参数（检查键、值、缓存状态）
func ValidateWriteOp(key, value []byte, initialized, closed bool) error {
	if err := ValidateBasicOp(key, initialized, closed); err != nil {
		return err
	}
	if err := ValidateValue(value); err != nil {
		return err
	}
	return nil
}

// ValidateWriteWithTTLOp 验证带 TTL 的写操作参数
func ValidateWriteWithTTLOp(key, value []byte, ttl time.Duration, initialized, closed bool) error {
	if err := ValidateWriteOp(key, value, initialized, closed); err != nil {
		return err
	}
	if err := ValidateTTL(ttl); err != nil {
		return err
	}
	return nil
}