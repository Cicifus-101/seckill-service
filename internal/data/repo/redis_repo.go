package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"

	"seckill-service/internal/biz"
)

type redisRepo struct {
	rdb       *redis.Client
	luaScript *redis.Script
	log       *log.Helper
}

// NewRedisRepo 创建 Redis 仓储
func NewRedisRepo(rdb *redis.Client, logger log.Logger) biz.CacheRepo {
	script := `
    if redis.call('exists', KEYS[2]) == 1 then
        return -1
    end
    local stock = tonumber(redis.call('get', KEYS[1]))
    if stock == nil or stock < tonumber(ARGV[1]) then
        return 0
    end
    redis.call('decrby', KEYS[1], ARGV[1])
    redis.call('setex', KEYS[2], ARGV[3], 1)
    return 1
    `

	return &redisRepo{
		rdb:       rdb,
		luaScript: redis.NewScript(script),
		log:       log.NewHelper(log.With(logger, "module", "repo/redis")),
	}
}

// BatchSetProducts 批量设置商品缓存（预热）
func (r *redisRepo) BatchSetProducts(ctx context.Context, products []*biz.CachedSeckillProduct) error {
	if len(products) == 0 {
		return nil
	}
	r.log.WithContext(ctx).Infof("开始批量预热缓存, 商品数量=%d", len(products))
	// 分批处理，避免一次性发送过多命令（每批500个）
	batchSize := 500
	totalBatches := (len(products) + batchSize - 1) / batchSize

	for i := 0; i < len(products); i += batchSize {
		end := i + batchSize
		if end > len(products) {
			end = len(products)
		}

		batchNum := i/batchSize + 1
		if err := r.batchSetProducts(ctx, products[i:end]); err != nil {
			r.log.WithContext(ctx).Warnf("批量预热第%d/%d批失败: %v", batchNum, totalBatches, err)
			// 继续处理剩余批次，不中断整个预热流程
			continue
		}
		r.log.WithContext(ctx).Debugf("批量预热第%d/%d批完成", batchNum, totalBatches)
	}

	r.log.WithContext(ctx).Infof("批量预热完成, 商品数量=%d", len(products))
	return nil
}

// 批量设置商品缓存
func (r *redisRepo) batchSetProducts(ctx context.Context, products []*biz.CachedSeckillProduct) error {
	pipe := r.rdb.Pipeline()
	for _, p := range products {
		key := fmt.Sprintf("seckill:product:%d:%d", p.ProductID, p.ActivityID)
		data, err := json.Marshal(p)
		if err != nil {
			r.log.WithContext(ctx).Warnf("序列化商品失败: skuID=%d, err=%v", p.SkuID, err)
			continue
		}
		// 防止雪崩
		ttl := time.Duration(2*3600+rand.Int63n(600)) * time.Second
		pipe.SetEX(ctx, key, data, ttl)

		stockKey := fmt.Sprintf("seckill:stock:%d", p.SkuID)
		pipe.Set(ctx, stockKey, p.AvailableStock, 0)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisRepo) GetProductList(ctx context.Context, activityID int64, page, pageSize, sortType int32) (*biz.SeckillProductsResult, error) {
	key := fmt.Sprintf("seckill:products:activity:%d:page:%d:size:%d:sort:%d",
		activityID, page, pageSize, sortType)

	data, err := r.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}

	var result biz.SeckillProductsResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *redisRepo) SetProductList(ctx context.Context, activityID int64, page, pageSize, sortType int32, data *biz.SeckillProductsResult, ttl time.Duration) error {
	key := fmt.Sprintf("seckill:products:activity:%d:page:%d:size:%d:sort:%d",
		activityID, page, pageSize, sortType)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	ttl = addRandomJitter(ttl)
	return r.rdb.SetEX(ctx, key, jsonData, ttl).Err()
}

// GetStock 获取库存
func (r *redisRepo) GetStock(ctx context.Context, skuID uint64) (int64, error) {
	key := fmt.Sprintf("seckill:stock:%d", skuID)
	return r.rdb.Get(ctx, key).Int64()
}

// SetStock 设置库存
func (r *redisRepo) SetStock(ctx context.Context, skuID uint64, stock int64) error {
	key := fmt.Sprintf("seckill:stock:%d", skuID)
	return r.rdb.Set(ctx, key, stock, 0).Err()
}

// DeductStock 原子扣减库存
func (r *redisRepo) DeductStock(ctx context.Context, skuID, userID uint64, quantity int) (int, error) {
	stockKey := fmt.Sprintf("seckill:stock:%d", skuID)
	userKey := fmt.Sprintf("seckill:user:%d:%d", skuID, userID)

	result, err := r.luaScript.Run(ctx, r.rdb,
		[]string{stockKey, userKey},
		quantity, 1, 900,
	).Int()

	return result, err
}

// RollbackStock 回滚库存
func (r *redisRepo) RollbackStock(ctx context.Context, skuID uint64, quantity int) error {
	stockKey := fmt.Sprintf("seckill:stock:%d", skuID)
	return r.rdb.IncrBy(ctx, stockKey, int64(quantity)).Err()
}

// CheckUserBuy 检查用户购买
func (r *redisRepo) CheckUserBuy(ctx context.Context, skuID, userID uint64) (bool, error) {
	key := fmt.Sprintf("seckill:user:%d:%d", skuID, userID)
	exist, err := r.rdb.Exists(ctx, key).Result()
	return exist == 1, err
}

// MarkUserBuy 标记用户购买
func (r *redisRepo) MarkUserBuy(ctx context.Context, skuID, userID uint64, ttl int64) error {
	key := fmt.Sprintf("seckill:user:%d:%d", skuID, userID)
	return r.rdb.SetEX(ctx, key, 1, time.Duration(ttl)*time.Second).Err()
}

// RemoveUserBuy 删除用户购买标记
func (r *redisRepo) RemoveUserBuy(ctx context.Context, skuID, userID uint64) error {
	key := fmt.Sprintf("seckill:user:%d:%d", skuID, userID)
	return r.rdb.Del(ctx, key).Err()
}

// GetCurrentActivity 获取当前活动
func (r *redisRepo) GetCurrentActivity(ctx context.Context) (*biz.Activity, error) {
	key := "seckill:current:activity"
	data, err := r.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}

	var activity biz.Activity
	if err := json.Unmarshal(data, &activity); err != nil {
		return nil, err
	}
	return &activity, nil
}

// SetCurrentActivity 设置当前活动
func (r *redisRepo) SetCurrentActivity(ctx context.Context, activity *biz.Activity, ttl time.Duration) error {
	key := "seckill:current:activity"
	data, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	return r.rdb.SetEX(ctx, key, data, ttl).Err()
}

// GetProductDetail 获取商品详情缓存
func (r *redisRepo) GetProductDetail(ctx context.Context, productID, activityID uint64) (*biz.SeckillProductDetail, error) {
	key := fmt.Sprintf("seckill:product:%d:%d", productID, activityID)
	data, err := r.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var product biz.SeckillProductDetail
	if err := json.Unmarshal(data, &product); err != nil {
		return nil, err
	}
	return &product, nil
}

// SetProductDetail 设置商品详情缓存
func (r *redisRepo) SetProductDetail(ctx context.Context, productID, activityID uint64, detail *biz.SeckillProductDetail, ttl time.Duration) error {
	key := fmt.Sprintf("seckill:product:%d:%d", productID, activityID)
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	ttl = addRandomJitter(ttl)
	return r.rdb.SetEX(ctx, key, data, ttl).Err()
}

// Get 通用获取缓存
func (r *redisRepo) Get(ctx context.Context, key string) (string, error) {
	return r.rdb.Get(ctx, key).Result()
}

// Set 通用设置缓存
func (r *redisRepo) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return r.rdb.SetEX(ctx, key, value, ttl).Err()
}

// Del 通用删除缓存
func (r *redisRepo) Del(ctx context.Context, keys ...string) error {
	return r.rdb.Del(ctx, keys...).Err()
}

func (r *redisRepo) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	return r.rdb.SetNX(ctx, key, value, ttl).Result()
}

// addRandomJitter 添加随机TTL偏移，防止缓存雪崩
func addRandomJitter(baseTTL time.Duration) time.Duration {
	// 随机偏移 ±10%
	jitter := time.Duration(rand.Int63n(int64(baseTTL/5))) - baseTTL/10
	result := baseTTL + jitter
	if result < 0 {
		result = baseTTL
	}
	return result
}
