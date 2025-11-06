/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 23:23:11
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:21:23
 * @FilePath: \go-cachex\ristretto_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRistrettoHandler(t *testing.T) {
	t.Run("Basic Operations", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		key := []byte("testKey")
		value := []byte("testValue")

		// Set and Get
		assert.NoError(handler.Set(key, value), "Set should succeed")
		got, err := handler.Get(key)
		assert.NoError(err, "Get should succeed")
		assert.Equal(value, got, "Get should return set value")

		// GetTTL
		ttl, err := handler.GetTTL(key)
		assert.NoError(err, "GetTTL should succeed")
		assert.Equal(time.Duration(0), ttl, "TTL should be 0 for non-TTL set")

		// Delete
		assert.NoError(handler.Del(key), "Del should succeed")
		_, err = handler.Get(key)
		assert.ErrorIs(err, ErrNotFound, "Get deleted key should return ErrNotFound")
	})

	t.Run("Empty Key/Value", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create handler")
		defer handler.Close()

		// Empty key
		value := []byte("value")
		assert.NoError(handler.Set([]byte{}, value), "Set empty key should succeed")
		got, err := handler.Get([]byte{})
		assert.NoError(err, "Get empty key should succeed")
		assert.Equal(value, got, "Get empty key should return correct value")

		// Empty value
		key := []byte("key")
		assert.NoError(handler.Set(key, []byte{}), "Set empty value should succeed")
		got, err = handler.Get(key)
		assert.NoError(err, "Get key with empty value should succeed")
		assert.Empty(got, "Get should return empty value")
	})

	t.Run("Value Updates", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create handler")
		defer handler.Close()

		key := []byte("key")
		value1 := []byte("value1")
		value2 := []byte("value2")

		// First set
		assert.NoError(handler.Set(key, value1), "First set should succeed")
		got, err := handler.Get(key)
		assert.NoError(err, "Get after first set should succeed")
		assert.Equal(value1, got, "Should get first value")

		// Update
		assert.NoError(handler.Set(key, value2), "Update should succeed")
		got, err = handler.Get(key)
		assert.NoError(err, "Get after update should succeed")
		assert.Equal(value2, got, "Should get updated value")
	})

	t.Run("Non-existent Keys", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create handler")
		defer handler.Close()

		// Get non-existent
		_, err = handler.Get([]byte("non-existent"))
		assert.ErrorIs(err, ErrNotFound, "Get non-existent key should return ErrNotFound")

		// Delete non-existent
		assert.NoError(handler.Del([]byte("non-existent")), "Del non-existent key should succeed")
	})
}

func TestRistrettoHandlerWithTTL(t *testing.T) {
	t.Run("TTL Operations", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		key := []byte("testKeyWithTTL")
		value := []byte("testValueWithTTL")
		ttl := 5 * time.Second

		assert.NoError(handler.SetWithTTL(key, value, ttl), "SetWithTTL should succeed")

		got, err := handler.Get(key)
		assert.NoError(err, "Get should succeed")
		assert.Equal(value, got, "Get should return set value")

		gotTTL, err := handler.GetTTL(key)
		assert.NoError(err, "GetTTL should succeed")
		assert.True(gotTTL <= ttl && gotTTL > 0, "TTL should be between 0 and 5s")
	})

	t.Run("TTL Expiration", func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		key := []byte("expireKey")
		value := []byte("expireValue")
		ttl := 1 * time.Second

		assert.NoError(handler.SetWithTTL(key, value, ttl), "SetWithTTL should succeed")
		time.Sleep(2 * time.Second) // Wait for expiration

		_, err = handler.Get(key)
		assert.ErrorIs(err, ErrNotFound, "Get after TTL expiration should return ErrNotFound")
	})
}

func TestRistrettoLargeData(t *testing.T) {
	t.Run("Large Data Operations", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		const numItems = 10000
		const checkInterval = 1000

		// Batch set
		for i := 0; i < numItems; i++ {
			key := []byte("key" + strconv.Itoa(i))
			value := []byte("value" + strconv.Itoa(i))
			assert.NoError(handler.Set(key, value), "Set should succeed for item %d", i)
		}

		// Sample verification
		for i := 0; i < numItems; i += checkInterval {
			key := []byte("key" + strconv.Itoa(i))
			expectedValue := []byte("value" + strconv.Itoa(i))
			
			got, err := handler.Get(key)
			if assert.NoError(err, "Get should succeed for item %d", i) {
				assert.Equal(expectedValue, got, "Values should match for item %d", i)
			}
		}
	})

	t.Run("Concurrent Access", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		const numGoroutines = 10
		const numOpsPerGoroutine = 1000
		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(routineID int) {
				base := routineID * numOpsPerGoroutine
				for j := 0; j < numOpsPerGoroutine; j++ {
					key := []byte(strconv.Itoa(base + j))
					value := []byte("value" + strconv.Itoa(base + j))
					
					assert.NoError(handler.Set(key, value), "Concurrent Set should succeed")
					
					if got, err := handler.Get(key); assert.NoError(err) {
						assert.Equal(value, got, "Concurrent Get should return correct value")
					}
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
	})
}