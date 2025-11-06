/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 23:27:58
 * @FilePath: \go-cachex\ctxcache_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetOrCompute_Singleflight(t *testing.T) {
    rh, err := NewDefaultRistrettoHandler()
    if err != nil {
        t.Fatalf("failed create ristretto: %v", err)
    }
    defer rh.Close()

    c := NewCtxCache(rh)

    var calls int32
    loader := func(ctx context.Context) ([]byte, error) {
        atomic.AddInt32(&calls, 1)
        // 模拟慢计算
        time.Sleep(100 * time.Millisecond)
        return []byte("value"), nil
    }

    concurrency := 10
    wg := sync.WaitGroup{}
    wg.Add(concurrency)

    for i := 0; i < concurrency; i++ {
        go func() {
            defer wg.Done()
            ctx := context.Background()
            v, err := c.GetOrCompute(ctx, []byte("k1"), 0, loader)
            if err != nil {
                t.Errorf("unexpected err: %v", err)
                return
            }
            if string(v) != "value" {
                t.Errorf("unexpected val: %s", string(v))
            }
        }()
    }

    wg.Wait()

    if got := atomic.LoadInt32(&calls); got != 1 {
        t.Fatalf("loader should be called once, called=%d", got)
    }
}

func TestGetOrCompute_CtxCancel(t *testing.T) {
    rh, err := NewDefaultRistrettoHandler()
    if err != nil {
        t.Fatalf("failed create ristretto: %v", err)
    }
    defer rh.Close()

    c := NewCtxCache(rh)

    loader := func(ctx context.Context) ([]byte, error) {
        select {
        case <-time.After(200 * time.Millisecond):
            return []byte("v"), nil
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }

    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()

    _, err = c.GetOrCompute(ctx, []byte("k-cancel"), 0, loader)
    if err == nil {
        t.Fatalf("expected context error, got nil")
    }
}

func TestGetOrCompute_TTL(t *testing.T) {
    rh, err := NewDefaultRistrettoHandler()
    if err != nil {
        t.Fatalf("failed create ristretto: %v", err)
    }
    defer rh.Close()

    c := NewCtxCache(rh)

    var calls int32
    loader := func(ctx context.Context) ([]byte, error) {
        atomic.AddInt32(&calls, 1)
        return []byte("vttl"), nil
    }

    // 第一次填充，TTL = 100ms
    v, err := c.GetOrCompute(context.Background(), []byte("k-ttl"), 100*time.Millisecond, loader)
    if err != nil || string(v) != "vttl" {
        t.Fatalf("unexpected first load: %v %v", v, err)
    }

    // 立即再次读取，不应该触发 loader
    v2, err := c.GetOrCompute(context.Background(), []byte("k-ttl"), 100*time.Millisecond, loader)
    if err != nil || string(v2) != "vttl" {
        t.Fatalf("unexpected second load: %v %v", v2, err)
    }

    if got := atomic.LoadInt32(&calls); got != 1 {
        t.Fatalf("loader should be called once so far, called=%d", got)
    }

    // 等待 TTL 过期
    time.Sleep(150 * time.Millisecond)

    _, err = c.GetOrCompute(context.Background(), []byte("k-ttl"), 100*time.Millisecond, loader)
    if err != nil {
        t.Fatalf("unexpected err after ttl: %v", err)
    }

    if got := atomic.LoadInt32(&calls); got != 2 {
        t.Fatalf("loader should be called twice after ttl expiry, called=%d", got)
    }
}
