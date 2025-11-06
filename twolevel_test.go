/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:35:15
 * @FilePath: \go-cachex\sharded.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"testing"
)

func TestTwoLevel_GetFallbackAndPromote(t *testing.T) {
    l1 := NewLRUHandler(1)              // very small L1
    l2 := NewExpiringHandler(0)         // L2 holds more
    defer l1.Close()
    defer l2.Close()

    // put value only in L2
    if err := l2.Set([]byte("k"), []byte("v")); err != nil {
        t.Fatalf("l2 set err: %v", err)
    }

    two := NewTwoLevelHandler(l1, l2, true)
    defer two.Close()

    // Get should return from L2 and promote to L1
    v, err := two.Get([]byte("k"))
    if err != nil || string(v) != "v" {
        t.Fatalf("unexpected get: %v %v", v, err)
    }

    // Now L1 should have it
    if v2, err := l1.Get([]byte("k")); err != nil || string(v2) != "v" {
        t.Fatalf("L1 should be promoted, got: %v %v", v2, err)
    }
}
