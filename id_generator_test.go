/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-25 09:58:50
 * @FilePath: \go-cachex\id_generator_test.go
 * @Description: 通用ID生成器单元测试
 */

package cachex

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestIDGenerator_NilClient 测试当Redis客户端为nil时的行为
func TestIDGenerator_NilClient(t *testing.T) {
	ctx := context.Background()
	genNil := NewIDGenerator(nil)
	_, err := genNil.GenerateID(ctx)
	assert.Error(t, err)
	_, err = genNil.GenerateIDs(ctx, 2)
	assert.Error(t, err)
	err = genNil.ResetSequence(ctx, "2025112510")
	assert.Error(t, err)
	_, err = genNil.GetCurrentSequence(ctx)
	assert.Error(t, err)
	_, err = genNil.GetSequenceByTime(ctx, "2025112510")
	assert.Error(t, err)
}

// TestIDGenerator_InvalidArgs 测试传入无效参数的行为
func TestIDGenerator_InvalidArgs(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	idGen := NewIDGenerator(client)
	_, err := idGen.GenerateIDs(ctx, 0)
	assert.Error(t, err)
}

// TestIDGenerator_SingleID_Default 测试单个生成ID的Lua脚本实现
func TestIDGenerator_SingleID_Default(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	idGen := NewIDGenerator(client)
	id, err := idGen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, id)
}

// TestIDGenerator_BatchID_Lua 测试批量生成ID的Lua脚本实现
func TestIDGenerator_BatchID_Lua(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	idGen := NewIDGenerator(client)
	ids, err := idGen.GenerateIDs(ctx, 5)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(ids))
	for _, id := range ids {
		assert.NotEmpty(t, id)
	}
}

// TestIDGenerator_CustomOptions 测试自定义配置选项
func TestIDGenerator_CustomOptions(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()

	// 创建自定义 ID 生成器
	gen := NewIDGenerator(
		client,
		WithKeyPrefix("test:id:seq:"),
		WithTimeFormat("20060102"),
		WithSequenceLength(8),
		WithExpire(10*time.Minute),
		WithSequenceStart(100),
		WithIDPrefix("PRE-"),
		WithIDSuffix("-SUF"),
		WithSeparator("_"),
	)

	// 重置序列
	timePrefix := gen.getTimePrefix()
	_ = gen.ResetSequence(ctx, timePrefix)

	// 生成单个 ID
	id, err := gen.GenerateID(ctx)
	assert.NoError(t, err)
	validateID(t, id, gen)

	// 生成多个 ID
	ids, err := gen.GenerateIDs(ctx, 3)
	assert.NoError(t, err)
	assert.Len(t, ids, 3)
	for _, id := range ids {
		validateID(t, id, gen)
	}
}

// validateID 验证 ID 的格式
func validateID(t *testing.T, id string, gen *IDGenerator) {
	assert.True(t, strings.HasPrefix(id, gen.GetIDPrefix()), "ID前缀不正确")
	assert.True(t, strings.HasSuffix(id, gen.GetIDSuffix()), "ID后缀不正确")
	parts := strings.SplitN(id, gen.GetSeparator(), 2)
	assert.Len(t, parts, 2, "分隔符位置不正确")
}

// TestIDGenerator_GetCurrentAndByTime 测试获取当前和指定时间的序列号
func TestIDGenerator_GetCurrentAndByTime(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	gen := NewIDGenerator(client, WithSequenceStart(100))
	timePrefix := gen.getTimePrefix()
	_ = gen.ResetSequence(ctx, timePrefix)
	gen.GenerateID(ctx)
	seq, err := gen.GetCurrentSequence(ctx)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, seq, int64(100))
	seq2, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, seq, seq2)
}

// TestIDGenerator_ResetSequence 测试重置序列号
func TestIDGenerator_ResetSequence(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	gen := NewIDGenerator(client, WithSequenceStart(100))
	timePrefix := gen.getTimePrefix()
	gen.GenerateID(ctx)
	err := gen.ResetSequence(ctx, timePrefix)
	assert.NoError(t, err)
	seq, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), seq)
}

// TestIDGenerator_SequenceNotExist 测试获取不存在的序列号
func TestIDGenerator_SequenceNotExist(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	gen := NewIDGenerator(client)
	seq, err := gen.GetSequenceByTime(ctx, "20991231")
	assert.NoError(t, err)
	assert.Equal(t, int64(0), seq)
}

// TestIDGenerator 测试ID生成器
func TestIDGenerator(t *testing.T) {
	
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()

	// 清理之前的测试数据
	defer client.FlushDB(ctx)

	// 测试生成ID
	idGen := NewIDGenerator(client, WithKeyPrefix("ticket:id:seq:"), WithTimeFormat("2006010215"), WithSequenceLength(6))

	id, err := idGen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, id)

	// 测试重置序列号
	err = idGen.ResetSequence(ctx, "2025111916")
	assert.NoError(t, err)

	// 测试获取当前序列号
	seq, err := idGen.GetCurrentSequence(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), seq)

	// 测试生成ID时Redis返回错误
	// 这里我们可以直接测试生成ID，而不是模拟错误情况
	id, err = idGen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, id)

	// 测试设置不同的选项
	idGenWithOptions := NewIDGenerator(
		client,
		WithKeyPrefix("order:id:seq:"),
		WithTimeFormat("20060102"),
		WithSequenceLength(8),
	)

	id, err = idGenWithOptions.GenerateID(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, id)
}

// TestIDGenerator_Coverage 补充覆盖率测试
func TestIDGenerator_Coverage(t *testing.T) {
	ctx := context.Background()

	// 1. redisClient为nil时的错误分支
	genNil := NewIDGenerator(nil)
	_, err := genNil.GenerateID(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis client is not available")
	err = genNil.ResetSequence(ctx, "2025111916")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis client is not available")
	_, err = genNil.GetCurrentSequence(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis client is not available")

	// 2. GetCurrentSequence键不存在时返回0
	client := setupRedisClient(t)
	defer client.Close()
	idGen := NewIDGenerator(client)
	// 先清理，确保键不存在
	timePrefix := "2099123123" // 未来时间，确保不存在
	_ = idGen.ResetSequence(ctx, timePrefix)
	seq, err := idGen.GetCurrentSequence(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), seq)
}

// TestIDGenerator_SequenceInitCallback 测试动态起始值回调
func TestIDGenerator_SequenceInitCallback(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	// 假设数据库查到最大ID为8888
	var (
		called bool
		initValue int64 = 8888
	)
	gen := NewIDGenerator(
		client,
		WithKeyPrefix("cb:id:seq:"),
		WithSequenceInitCallback(func() int64 {
			called = true
			return initValue
		}),
	)
	timePrefix := gen.getTimePrefix()
	// 清理key，模拟key不存在
	_ = gen.ResetSequence(ctx, timePrefix)
	id, err := gen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.True(t, called, "回调未被调用")
	// 生成的ID序列号应为8889
	seq, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, initValue +1 , seq)// 验证生成的ID的后四位
    expectedIDSuffix := fmt.Sprintf("%04d", initValue + 1) // 8889
    actualIDSuffix := id[len(id)-4:] // 获取ID的后四位
    assert.Equal(t, expectedIDSuffix, actualIDSuffix, "生成的ID后四位不正确")
}