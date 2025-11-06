/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:36:15
 * @FilePath: \go-cachex\sharded_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSharded_Basic(t *testing.T) {
    // factory: per-shard LRU with small capacity
    factory := func() Handler {
        return NewLRUHandler(100)
    }
    s := NewShardedHandler(factory, 8)
    defer s.Close()

    // concurrent writes
    wg := sync.WaitGroup{}
    for i := 0; i < 500; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            k := []byte("key-" + strconv.Itoa(i%50))
            _ = s.SetWithTTL(k, []byte("v"), 100*time.Millisecond)
        }(i)
    }
    wg.Wait()

    // reads
    for i := 0; i < 50; i++ {
        k := []byte("key-" + strconv.Itoa(i))
        if v, err := s.Get(k); err != nil {
            t.Logf("key %d may be missing: %v", i, err)
        } else {
            _ = v
        }
    }
}
