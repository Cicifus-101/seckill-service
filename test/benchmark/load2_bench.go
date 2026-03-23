package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	v1 "seckill-service/api/seckill/v1"
)

const (
	targetGRPC  = "127.0.0.1:9000"
	concurrency = 50  // 并发用户数
	totalUsers  = 500 // 总模拟用户数
)

type Stats struct {
	name         string
	successCount int64
	failCount    int64
	latencies    []time.Duration
	mu           sync.Mutex
}

func (s *Stats) record(d time.Duration, err error) {
	if err != nil {
		atomic.AddInt64(&s.failCount, 1)
	} else {
		atomic.AddInt64(&s.successCount, 1)
		s.mu.Lock()
		s.latencies = append(s.latencies, d)
		s.mu.Unlock()
	}
}

func main() {
	conn, err := grpc.Dial(targetGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	client := v1.NewSeckillClient(conn)

	// 初始化各接口统计
	apiStats := map[string]*Stats{
		"GetActivity": {name: "获取当前活动"},
		"ListProduct": {name: "查询商品列表"},
		"Detail":      {name: "查询商品详情"},
		"CreateOrder": {name: "创建秒杀订单"},
		"GetResult":   {name: "获取秒杀结果"},
		"Pay":         {name: "支付秒杀订单"},
	}

	fmt.Printf("开始全链路压测: 模拟用户=%d, 并发=%d\n", totalUsers, concurrency)
	startTime := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for i := 0; i < totalUsers; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(uid int64) {
			defer wg.Done()
			defer func() { <-sem }()

			ctx := context.Background()

			// 1. 获取当前活动
			start := time.Now()
			_, err := client.GetCurrentActivity(ctx, &v1.GetCurrentActivityRequest{})
			apiStats["GetActivity"].record(time.Since(start), err)

			// 2. 查询商品列表
			start = time.Now()
			_, err = client.SeckillProducts(ctx, &v1.SeckillProductsRequest{UserId: uid, Page: 1, PageSize: 10})
			apiStats["ListProduct"].record(time.Since(start), err)

			// 3. 查询商品详情
			start = time.Now()
			_, err = client.SeckillProductDetail(ctx, &v1.SeckillProductDetailRequest{UserId: uid, ProductId: 1, ActivityId: 1})
			apiStats["Detail"].record(time.Since(start), err)

			// 4. 创建订单 (核心：使用优惠券 ID=1)
			orderNo := ""
			start = time.Now()
			res, err := client.CreateSeckillOrder(ctx, &v1.CreateSeckillOrderRequest{
				UserId:     uid,
				SkuId:      1001,
				ActivityId: 1,
				ProductId:  1,
				Quantity:   1,
				AddressId:  uid, // 对应生成的数据库 ID
				CouponId:   1,   // 使用下面 SQL 插入的优惠券
				RequestId:  fmt.Sprintf("REQ_%d", uid),
			})
			apiStats["CreateOrder"].record(time.Since(start), err)
			if err == nil {
				orderNo = res.OrderNo
			}

			// 5. 获取秒杀结果 (轮询模拟)
			if orderNo != "" {
				start = time.Now()
				_, err = client.GetSeckillResult(ctx, &v1.GetSeckillResultRequest{UserId: uid, RequestId: fmt.Sprintf("REQ_%d", uid)})
				apiStats["GetResult"].record(time.Since(start), err)

				// 6. 支付订单
				start = time.Now()
				_, err = client.PaySeckillOrder(ctx, &v1.PaySeckillOrderRequest{
					OrderNo:     orderNo,
					UserId:      uid,
					PayPlatform: 1, // 支付宝
				})
				apiStats["Pay"].record(time.Since(start), err)
			}
		}(int64(i + 1))
	}

	wg.Wait()
	printReport(apiStats, time.Since(startTime))
}

func printReport(stats map[string]*Stats, totalTime time.Duration) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Printf("全链路压测报告 (总耗时: %v)\n", totalTime)
	keys := []string{"GetActivity", "ListProduct", "Detail", "CreateOrder", "GetResult", "Pay"}
	for _, k := range keys {
		s := stats[k]
		sort.Slice(s.latencies, func(i, j int) bool { return s.latencies[i] < s.latencies[j] })
		p99 := "N/A"
		if len(s.latencies) > 0 {
			p99 = fmt.Sprintf("%v", s.latencies[len(s.latencies)*99/100])
		}
		fmt.Printf("[%s] 成功:%-4d 失败:%-4d P99:%-10s\n", s.name, s.successCount, s.failCount, p99)
	}
	fmt.Println(strings.Repeat("=", 60))
}
