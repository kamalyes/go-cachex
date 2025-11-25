/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 22:51:23
 * @FilePath: \go-cachex\id_generator_test.go
 * @Description: 通用ID生成器单元测试
 */

package cachex

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
	"time"
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
	idGen := NewIDGenerator(client).WithKeyPrefix("test:single:")
	timePrefix := idGen.getTimePrefix()
	_ = idGen.ForceResetSequence(ctx, timePrefix)

	id, err := idGen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, id)

	// 验证 Redis 中的值
	seq, err := idGen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), seq, "Redis中的序列号应该是1")

	// 验证生成的ID包含正确的序列号
	expectedSeq := fmt.Sprintf("%06d", 1)
	assert.True(t, strings.HasSuffix(id, expectedSeq), "生成的ID应该以000001结尾")
}

// TestIDGenerator_BatchID_Lua 测试批量生成ID的Lua脚本实现
func TestIDGenerator_BatchID_Lua(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	idGen := NewIDGenerator(client).WithKeyPrefix("test:batch:")
	timePrefix := idGen.getTimePrefix()
	_ = idGen.ForceResetSequence(ctx, timePrefix)

	ids, err := idGen.GenerateIDs(ctx, 5)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(ids))
	for i, id := range ids {
		assert.NotEmpty(t, id)
		// 验证每个ID的序列号是递增的
		expectedSeq := fmt.Sprintf("%06d", i+1)
		assert.True(t, strings.HasSuffix(id, expectedSeq), fmt.Sprintf("第%d个ID应该以%s结尾", i+1, expectedSeq))
	}

	// 验证 Redis 中的最终序列号
	seq, err := idGen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(5), seq, "批量生成5个ID后，Redis中的序列号应该是5")
}

// TestIDGenerator_CustomOptions 测试自定义配置选项
func TestIDGenerator_CustomOptions(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()

	// 创建自定义 ID 生成器
	gen := NewIDGenerator(client).
		WithKeyPrefix("test:id:seq:").
		WithTimeFormat("20060102").
		WithSequenceLength(8).
		WithExpire(10 * time.Minute).
		WithSequenceStart(100).
		WithIDPrefix("PRE-").
		WithIDSuffix("-SUF").
		WithSeparator("_")

	// 重置序列
	timePrefix := gen.getTimePrefix()
	_ = gen.ForceResetSequence(ctx, timePrefix)

	// 生成单个 ID
	id, err := gen.GenerateID(ctx)
	assert.NoError(t, err)
	validateID(t, id, gen)

	// 验证 Redis 中的序列号（起始值是100）
	seq, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(100), seq, "第一次生成ID后，序列号应该是100")

	// 生成多个 ID
	ids, err := gen.GenerateIDs(ctx, 3)
	assert.NoError(t, err)
	assert.Len(t, ids, 3)
	for _, id := range ids {
		validateID(t, id, gen)
	}

	// 验证 Redis 中的最终序列号（100 + 3 = 103）
	seq, err = gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(103), seq, "批量生成3个ID后，序列号应该是103")
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
	gen := NewIDGenerator(client).WithSequenceStart(100).WithKeyPrefix("test:current:")
	timePrefix := gen.getTimePrefix()
	_ = gen.ForceResetSequence(ctx, timePrefix)

	id, err := gen.GenerateID(ctx)
	assert.NoError(t, err)

	seq, err := gen.GetCurrentSequence(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(100), seq, "第一次生成后序列号应该是100")

	seq2, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, seq, seq2, "两种方式获取的序列号应该一致")

	// 直接从 Redis 读取验证
	redisKey := gen.getRedisKey(timePrefix)
	redisVal, err := client.Get(ctx, redisKey).Int64()
	assert.NoError(t, err)
	assert.Equal(t, int64(100), redisVal, "Redis中存储的值应该是100")

	// 验证ID包含正确的序列号
	expectedSeq := fmt.Sprintf("%06d", 100)
	assert.True(t, strings.HasSuffix(id, expectedSeq), "生成的ID应该以000100结尾")
}

// TestIDGenerator_ResetSequence 测试安全的重置序列号（key已存在时不重置）
func TestIDGenerator_ResetSequence(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	gen := NewIDGenerator(client).WithSequenceStart(100).WithKeyPrefix("test:reset:")
	timePrefix := gen.getTimePrefix()
	_ = gen.ForceResetSequence(ctx, timePrefix)
	_, err := gen.GenerateID(ctx)
	assert.NoError(t, err)

	// 再次调用 ResetSequence，应该不会重置已有的序列号
	err = gen.ResetSequence(ctx, timePrefix)
	assert.NoError(t, err)
	seq, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(100), seq, "key已存在时，ResetSequence不应重置序列号")
}

// TestIDGenerator_ForceResetSequence 测试强制重置序列号
func TestIDGenerator_ForceResetSequence(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	gen := NewIDGenerator(client).WithSequenceStart(100).WithKeyPrefix("test:forcereset:")
	timePrefix := gen.getTimePrefix()
	_, err := gen.GenerateID(ctx)
	assert.NoError(t, err)

	// 强制重置
	err = gen.ForceResetSequence(ctx, timePrefix)
	assert.NoError(t, err)
	seq, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), seq, "ForceResetSequence应该删除key")
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
	idGen := NewIDGenerator(client).WithKeyPrefix("ticket:id:seq:").WithTimeFormat("2006010215").WithSequenceLength(6)
	timePrefix := idGen.getTimePrefix()
	_ = idGen.ForceResetSequence(ctx, timePrefix)

	id, err := idGen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, id)

	// 验证 Redis 中的序列号
	seq, err := idGen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), seq, "第一次生成ID后序列号应该是1")

	// 测试重置序列号
	err = idGen.ResetSequence(ctx, "2025111916")
	assert.NoError(t, err)

	// 测试获取当前序列号
	seq, err = idGen.GetCurrentSequence(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), seq)

	// 再次生成ID，序列号应该递增
	id2, err := idGen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, id2)

	// 验证序列号递增到2
	seq, err = idGen.GetCurrentSequence(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), seq, "第二次生成ID后序列号应该是2")

	// 验证两个ID不同
	assert.NotEqual(t, id, id2, "两次生成的ID应该不同")

	// 测试设置不同的选项
	idGenWithOptions := NewIDGenerator(client).
		WithKeyPrefix("order:id:seq:").
		WithTimeFormat("20060102").
		WithSequenceLength(8)

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
		called    bool
		initValue int64 = 8888
	)
	gen := NewIDGenerator(client).
		WithKeyPrefix("cb:id:seq:").
		WithSequenceInitCallback(func() int64 {
			called = true
			return initValue
		})
	timePrefix := gen.getTimePrefix()
	// 清理key，模拟key不存在
	_ = gen.ForceResetSequence(ctx, timePrefix)
	id, err := gen.GenerateID(ctx)
	assert.NoError(t, err)
	assert.True(t, called, "回调未被调用")
	// 生成的ID序列号应为8888（第一次INCR从8887->8888）
	seq, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, initValue, seq) // 第一次生成应该是8888
	// 验证生成的ID的后四位
	expectedIDSuffix := fmt.Sprintf("%04d", initValue) // 8888
	actualIDSuffix := id[len(id)-4:]                   // 获取ID的后四位
	assert.Equal(t, expectedIDSuffix, actualIDSuffix, "生成的ID后四位不正确")
}

// TestIDGenerator_NoReset_ContinuousIncrement 测试多次生成ID且间隙内不重置，序列号应持续递增
func TestIDGenerator_NoReset_ContinuousIncrement(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()
	gen := NewIDGenerator(client).WithKeyPrefix("test:noreset:")
	timePrefix := gen.getTimePrefix()
	_ = gen.ForceResetSequence(ctx, timePrefix)

	// 连续生成10个ID，不重置
	count := 10
	for i := 1; i <= count; i++ {
		id, err := gen.GenerateID(ctx)
		assert.NoError(t, err)
		assert.NotEmpty(t, id)
		expectedSeq := fmt.Sprintf("%06d", i)
		assert.True(t, strings.HasSuffix(id, expectedSeq), "第%d个ID应该以%s结尾", i, expectedSeq)
	}

	// 检查Redis中的序列号
	seq, err := gen.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(count), seq, "生成%d个ID后，序列号应该是%d", count, count)
}

// TestIDGenerator_DistributedProtection 测试分布式环境下的并发保护
func TestIDGenerator_DistributedProtection(t *testing.T) {
	ctx := context.Background()
	clientA := setupRedisClient(t)
	clientB := setupRedisClient(t)
	defer clientA.Close()
	defer clientB.Close()

	genA := NewIDGenerator(clientA).WithKeyPrefix("dist:id:seq:")
	genB := NewIDGenerator(clientB).WithKeyPrefix("dist:id:seq:")
	timePrefix := genA.getTimePrefix()

	// 清理环境
	_ = genA.ForceResetSequence(ctx, timePrefix)

	// 程序A先生成10个ID
	for i := 0; i < 10; i++ {
		_, err := genA.GenerateID(ctx)
		assert.NoError(t, err)
	}

	// 验证A生成了10个
	seq, err := genA.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(10), seq, "程序A应该生成了10个ID")

	// 程序B启动，调用ResetSequence（安全的初始化）
	err = genB.ResetSequence(ctx, timePrefix)
	assert.NoError(t, err)

	// 验证序列号没有被重置
	seq, err = genB.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(10), seq, "程序B的ResetSequence不应该重置已有序列号")

	// 程序B继续生成ID，应该从11开始
	id, err := genB.GenerateID(ctx)
	assert.NoError(t, err)
	expectedSeq := fmt.Sprintf("%06d", 11)
	assert.True(t, strings.HasSuffix(id, expectedSeq), "程序B生成的第一个ID应该是11")

	// 验证最终序列号
	seq, err = genB.GetSequenceByTime(ctx, timePrefix)
	assert.NoError(t, err)
	assert.Equal(t, int64(11), seq, "程序B生成一个ID后，总序列号应该是11")
}

// 业务场景模拟：订单服务调用链
// orderService -> paymentService -> notificationService
// 每个服务独立创建ID生成器实例，但使用相同的系统key前缀

// orderService 创建订单并生成订单ID
func createOrder(ctx context.Context, client redis.UniversalClient) (string, error) {
	// 业务方法A：创建自己的ID生成器实例
	idGen := NewIDGenerator(client).
		WithKeyPrefix("business:order:").
		WithTimeFormat("20060102").
		WithIDPrefix("ORD-")

	orderID, err := idGen.GenerateID(ctx)
	if err != nil {
		return "", err
	}

	// 调用支付服务
	paymentID, err := processPayment(ctx, client, orderID)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("订单:%s,支付:%s", orderID, paymentID), nil
}

// processPayment 处理支付并生成支付ID
func processPayment(ctx context.Context, client redis.UniversalClient, orderID string) (string, error) {
	// 业务方法B：重新创建ID生成器实例（模拟独立的微服务）
	idGen := NewIDGenerator(client).
		WithKeyPrefix("business:order:"). // 使用相同的系统key
		WithTimeFormat("20060102").
		WithIDPrefix("PAY-")

	paymentID, err := idGen.GenerateID(ctx)
	if err != nil {
		return "", err
	}

	// 调用通知服务
	notificationID, err := sendNotification(ctx, client, orderID, paymentID)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s,通知:%s", paymentID, notificationID), nil
}

// sendNotification 发送通知并生成通知ID
func sendNotification(ctx context.Context, client redis.UniversalClient, orderID, paymentID string) (string, error) {
	// 业务方法C：继续创建ID生成器实例
	idGen := NewIDGenerator(client).
		WithKeyPrefix("business:order:"). // 使用相同的系统key
		WithTimeFormat("20060102").
		WithIDPrefix("NOT-")

	notificationID, err := idGen.GenerateID(ctx)
	if err != nil {
		return "", err
	}

	return notificationID, nil
}

// TestIDGenerator_BusinessScenario 测试真实业务场景：多服务调用链生成ID
func TestIDGenerator_BusinessScenario(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()

	// 清理测试环境
	timePrefix := time.Now().Format("20060102")
	redisKey := fmt.Sprintf("business:order:%s", timePrefix)
	_ = client.Del(ctx, redisKey)

	// 场景1：第一次调用完整的订单流程（A->B->C）
	result1, err := createOrder(ctx, client)
	assert.NoError(t, err)
	assert.NotEmpty(t, result1)
	t.Logf("第一次调用结果: %s", result1)

	// 验证Redis中的序列号应该是3（订单1个+支付1个+通知1个）
	seq1, err := client.Get(ctx, redisKey).Int64()
	assert.NoError(t, err)
	assert.Equal(t, int64(3), seq1, "第一次完整调用链应该生成3个ID")

	// 场景2：第二次调用完整的订单流程
	result2, err := createOrder(ctx, client)
	assert.NoError(t, err)
	assert.NotEmpty(t, result2)
	t.Logf("第二次调用结果: %s", result2)

	// 验证Redis中的序列号应该是6（前3个+新3个）
	seq2, err := client.Get(ctx, redisKey).Int64()
	assert.NoError(t, err)
	assert.Equal(t, int64(6), seq2, "第二次完整调用链后应该累计6个ID")

	// 场景3：第三次调用
	result3, err := createOrder(ctx, client)
	assert.NoError(t, err)
	assert.NotEmpty(t, result3)
	t.Logf("第三次调用结果: %s", result3)

	// 验证最终序列号应该是9（前6个+新3个）
	finalSeq, err := client.Get(ctx, redisKey).Int64()
	assert.NoError(t, err)
	assert.Equal(t, int64(9), finalSeq, "三次完整调用链后应该累计9个ID")

	// 验证三次调用的结果都不同
	assert.NotEqual(t, result1, result2, "第一次和第二次调用结果应该不同")
	assert.NotEqual(t, result2, result3, "第二次和第三次调用结果应该不同")
	assert.NotEqual(t, result1, result3, "第一次和第三次调用结果应该不同")

	// 解析并验证ID的序列号是连续递增的
	// 第一次: ORD-20060102000001, PAY-20060102000002, NOT-20060102000003
	// 第二次: ORD-20060102000004, PAY-20060102000005, NOT-20060102000006
	// 第三次: ORD-20060102000007, PAY-20060102000008, NOT-20060102000009
	assert.Contains(t, result1, "000001", "第一次订单ID应包含序列号1")
	assert.Contains(t, result1, "000002", "第一次支付ID应包含序列号2")
	assert.Contains(t, result1, "000003", "第一次通知ID应包含序列号3")

	assert.Contains(t, result2, "000004", "第二次订单ID应包含序列号4")
	assert.Contains(t, result2, "000005", "第二次支付ID应包含序列号5")
	assert.Contains(t, result2, "000006", "第二次通知ID应包含序列号6")

	assert.Contains(t, result3, "000007", "第三次订单ID应包含序列号7")
	assert.Contains(t, result3, "000008", "第三次支付ID应包含序列号8")
	assert.Contains(t, result3, "000009", "第三次通知ID应包含序列号9")
}

// TestIDGenerator_ConcurrentBusinessScenario 测试并发业务场景：多个goroutine同时调用业务方法
func TestIDGenerator_ConcurrentBusinessScenario(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()

	// 清理测试环境
	timePrefix := time.Now().Format("20060102")
	redisKey := fmt.Sprintf("business:concurrent:%s", timePrefix)
	_ = client.Del(ctx, redisKey)

	// 并发参数
	goroutineCount := 10   // 10个并发goroutine
	callsPerGoroutine := 5 // 每个goroutine调用5次

	// 用于收集所有生成的ID
	type result struct {
		id  string
		err error
	}
	resultChan := make(chan result, goroutineCount*callsPerGoroutine)

	// 并发调用业务方法
	for i := 0; i < goroutineCount; i++ {
		go func(goroutineID int) {
			for j := 0; j < callsPerGoroutine; j++ {
				// 每个goroutine独立创建ID生成器实例并生成ID
				idGen := NewIDGenerator(client).
					WithKeyPrefix("business:concurrent:").
					WithTimeFormat("20060102").
					WithIDPrefix(fmt.Sprintf("G%d-", goroutineID))

				id, err := idGen.GenerateID(ctx)
				resultChan <- result{id: id, err: err}
			}
		}(i)
	}

	// 收集所有结果
	allIDs := make([]string, 0, goroutineCount*callsPerGoroutine)
	allSeqNumbers := make([]string, 0, goroutineCount*callsPerGoroutine)

	for i := 0; i < goroutineCount*callsPerGoroutine; i++ {
		res := <-resultChan
		assert.NoError(t, res.err, "生成ID不应该出错")
		assert.NotEmpty(t, res.id, "生成的ID不应该为空")
		allIDs = append(allIDs, res.id)

		// 提取序列号（假设格式是 Gx-20251126XXXXXX，取后6位）
		if len(res.id) >= 6 {
			seqNum := res.id[len(res.id)-6:]
			allSeqNumbers = append(allSeqNumbers, seqNum)
		}
	}
	close(resultChan)

	// 验证1: 所有ID应该唯一（没有重复）
	uniqueIDs := make(map[string]bool)
	for _, id := range allIDs {
		if uniqueIDs[id] {
			t.Errorf("发现重复ID: %s", id)
		}
		uniqueIDs[id] = true
	}
	assert.Equal(t, goroutineCount*callsPerGoroutine, len(uniqueIDs), "所有ID应该唯一")

	// 验证2: 所有序列号应该唯一
	uniqueSeqs := make(map[string]bool)
	for _, seq := range allSeqNumbers {
		if uniqueSeqs[seq] {
			t.Errorf("发现重复序列号: %s", seq)
		}
		uniqueSeqs[seq] = true
	}
	assert.Equal(t, goroutineCount*callsPerGoroutine, len(uniqueSeqs), "所有序列号应该唯一")

	// 验证3: Redis中的最终序列号应该等于总生成数量
	finalSeq, err := client.Get(ctx, redisKey).Int64()
	assert.NoError(t, err)
	expectedTotal := int64(goroutineCount * callsPerGoroutine)
	assert.Equal(t, expectedTotal, finalSeq, fmt.Sprintf("并发生成%d个ID后，序列号应该是%d", expectedTotal, expectedTotal))

	t.Logf("并发测试成功: %d个goroutine各调用%d次，共生成%d个唯一ID",
		goroutineCount, callsPerGoroutine, len(allIDs))
	t.Logf("序列号范围: %s ~ %s", allSeqNumbers[0], allSeqNumbers[len(allSeqNumbers)-1])
}

// TestIDGenerator_HighConcurrentBusinessChain 测试高并发下的完整业务调用链
func TestIDGenerator_HighConcurrentBusinessChain(t *testing.T) {
	ctx := context.Background()
	client := setupRedisClient(t)
	defer client.Close()

	// 清理测试环境
	timePrefix := time.Now().Format("20060102")
	redisKey := fmt.Sprintf("business:chain:%s", timePrefix)
	_ = client.Del(ctx, redisKey)

	// 并发参数
	concurrentOrders := 20 // 20个并发订单

	// 模拟高并发下的订单创建
	type orderResult struct {
		result string
		err    error
	}
	resultChan := make(chan orderResult, concurrentOrders)

	// 并发创建订单（每个订单会调用A->B->C链，生成3个ID）
	for i := 0; i < concurrentOrders; i++ {
		go func(orderNum int) {
			// 使用统一的key前缀模拟真实业务
			genA := NewIDGenerator(client).
				WithKeyPrefix("business:chain:").
				WithTimeFormat("20060102").
				WithIDPrefix("ORD-")

			orderID, err := genA.GenerateID(ctx)
			if err != nil {
				resultChan <- orderResult{err: err}
				return
			}

			// 调用B方法
			genB := NewIDGenerator(client).
				WithKeyPrefix("business:chain:").
				WithTimeFormat("20060102").
				WithIDPrefix("PAY-")

			payID, err := genB.GenerateID(ctx)
			if err != nil {
				resultChan <- orderResult{err: err}
				return
			}

			// 调用C方法
			genC := NewIDGenerator(client).
				WithKeyPrefix("business:chain:").
				WithTimeFormat("20060102").
				WithIDPrefix("NOT-")

			notifyID, err := genC.GenerateID(ctx)
			if err != nil {
				resultChan <- orderResult{err: err}
				return
			}

			result := fmt.Sprintf("订单%d[%s->%s->%s]", orderNum, orderID, payID, notifyID)
			resultChan <- orderResult{result: result, err: nil}
		}(i)
	}

	// 收集所有结果
	allResults := make([]string, 0, concurrentOrders)
	allGeneratedIDs := make([]string, 0, concurrentOrders*3) // 每个订单生成3个ID

	for i := 0; i < concurrentOrders; i++ {
		res := <-resultChan
		assert.NoError(t, res.err, "并发创建订单不应该出错")
		assert.NotEmpty(t, res.result, "订单结果不应该为空")
		allResults = append(allResults, res.result)

		// 提取所有ID
		parts := strings.Split(res.result, "[")
		if len(parts) > 1 {
			idPart := strings.TrimSuffix(parts[1], "]")
			ids := strings.Split(idPart, "->")
			allGeneratedIDs = append(allGeneratedIDs, ids...)
		}

		t.Logf("完成: %s", res.result)
	}
	close(resultChan)

	// 验证1: 所有订单结果应该唯一
	uniqueResults := make(map[string]bool)
	for _, result := range allResults {
		assert.False(t, uniqueResults[result], "订单结果不应该重复: %s", result)
		uniqueResults[result] = true
	}

	// 验证2: 所有生成的ID应该唯一
	uniqueIDs := make(map[string]bool)
	for _, id := range allGeneratedIDs {
		if uniqueIDs[id] {
			t.Errorf("发现重复ID: %s", id)
		}
		uniqueIDs[id] = true
	}
	expectedIDCount := concurrentOrders * 3
	assert.Equal(t, expectedIDCount, len(uniqueIDs), "应该生成%d个唯一ID", expectedIDCount)

	// 验证3: Redis中的最终序列号
	finalSeq, err := client.Get(ctx, redisKey).Int64()
	assert.NoError(t, err)
	assert.Equal(t, int64(expectedIDCount), finalSeq,
		"并发生成%d个订单（每个3个ID）后，序列号应该是%d", concurrentOrders, expectedIDCount)

	t.Logf("高并发业务链测试成功: %d个并发订单，共生成%d个唯一ID，最终序列号=%d",
		concurrentOrders, len(allGeneratedIDs), finalSeq)
}
