package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	pb "seckill-service/api/seckill/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	conn, err := grpc.Dial("127.0.0.1:9000", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	client := pb.NewSeckillClient(conn)

	var wg sync.WaitGroup
	for i := 1; i <= 100; i++ {
		uid := int64(2000 + i)
		reqID := fmt.Sprintf("oversell-%d-%d", uid, time.Now().UnixNano())

		wg.Add(1)
		go func(uid int64, reqID string) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := client.CreateSeckillOrder(ctx, &pb.CreateSeckillOrderRequest{
				UserId:     uid,
				SkuId:      3002,
				ActivityId: 3,
				ProductId:  4,
				Quantity:   1,
				CouponId:   0,
				AddressId:  1,
				ClientIp:   "127.0.0.1",
				RequestId:  reqID,
			})
			if err != nil {
				fmt.Println("ERR", uid, err)
				return
			}
			fmt.Println("OK", uid, resp)
		}(uid, reqID)
	}

	wg.Wait()
}
