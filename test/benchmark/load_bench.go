package main

/*import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	v1 "seckill-service/api/seckill/v1" // 请根据你的项目实际路径调整
)

const (
	targetGRPC    = "127.0.0.1:9000" // Kratos 默认 gRPC 端口
	concurrency   = 100              // 并发数
	totalRequests = 2000             // 总请求数
)

func main() {
	// 1. 建立 gRPC 连接
	conn, err := grpc.Dial(targetGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(fmt.Sprintf("连接失败: %v", err))
	}
	defer conn.Close()
	client := v1.NewSeckillClient(conn)

	var (
		wg           sync.WaitGroup
		successCount int64
		failCount    int64
		errMap       sync.Map // 用于分类统计错误原因
		latencies    = make([]time.Duration, 0, totalRequests)
		latencyCh    = make(chan time.Duration, totalRequests)
	)

	fmt.Printf("开始秒杀压测: 总请求=%d, 并发=%d\n", totalRequests, concurrency)
	sem := make(chan struct{}, concurrency)
	startTime := time.Now()

	// 2. 核心压测逻辑
	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		sem <- struct{}{} // 控制并发

		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			reqStart := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// 调用 Service 层接口，注入必填参数
			_, err := client.CreateSeckillOrder(ctx, &v1.CreateSeckillOrderRequest{
				UserId:     int64(idx + 1),             // 模拟不同用户
				SkuId:      1001,                       // 对应 biz 中的 SkuID
				ActivityId: 1,                          // 对应 biz 中的 ActivityID
				ProductId:  1,                          // 对应 biz 中的 ProductID
				Quantity:   1,                          // 购买数量
				AddressId:  1,                          // 必填：否则 service 层会返回 ArgumentInvalid
				RequestId:  fmt.Sprintf("REQ_%d", idx), // 必填：用于幂等校验
			})

			duration := time.Since(reqStart)

			if err != nil {
				atomic.AddInt64(&failCount, 1)
				st, _ := status.FromError(err)
				errMsg := st.Message()

				// 并发安全地记录错误类型
				val, _ := errMap.LoadOrStore(errMsg, new(int64))
				atomic.AddInt64(val.(*int64), 1)
			} else {
				atomic.AddInt64(&successCount, 1)
				latencyCh <- duration
			}
		}(i)
	}

	// 3. 异步关闭 Channel
	go func() {
		wg.Wait()
		close(latencyCh)
	}()

	// 4. 收集耗时数据
	for d := range latencyCh {
		latencies = append(latencies, d)
	}

	totalDuration := time.Since(startTime)

	// 5. 结果统计与展示
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	fmt.Println("\n" + strings.Repeat("=", 40))
	fmt.Printf("压测报告\n")
	fmt.Printf("测试耗时: %v\n", totalDuration)
	fmt.Printf("成功请求: %d\n", successCount)
	fmt.Printf("失败请求: %d\n", failCount)

	if len(latencies) > 0 {
		fmt.Printf("平均 QPS: %.2f\n", float64(len(latencies))/totalDuration.Seconds())
		fmt.Printf("P50 延迟: %v\n", latencies[len(latencies)*50/100])
		fmt.Printf("P95 延迟: %v\n", latencies[len(latencies)*95/100])
		fmt.Printf("P99 延迟: %v\n", latencies[len(latencies)*99/100])
	}

	fmt.Println("\n 错误分类汇总:")
	errMap.Range(func(k, v interface{}) bool {
		fmt.Printf("- [%s]: %d 次\n", k, atomic.LoadInt64(v.(*int64)))
		return true
	})
	fmt.Println(strings.Repeat("=", 40))
}*/
