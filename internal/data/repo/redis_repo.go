package repo

import (
	"context"
	"encoding/json"
	"fmt"
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

// GetProduct 获取商品缓存
func (r *redisRepo) GetProduct(ctx context.Context, skuID uint64) (*biz.CachedSeckillProduct, error) {
	key := fmt.Sprintf("seckill:sku:%d", skuID)
	data, err := r.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var product biz.CachedSeckillProduct
	if err := json.Unmarshal(data, &product); err != nil {
		return nil, err
	}
	return &product, nil
}

// SetProduct 设置商品缓存
func (r *redisRepo) SetProduct(ctx context.Context, skuID uint64, product *biz.CachedSeckillProduct, ttl int64) error {
	key := fmt.Sprintf("seckill:sku:%d", skuID) //这里面对redis的大key问题
	data, err := json.Marshal(product)
	if err != nil {
		return err
	}
	return r.rdb.SetEX(ctx, key, data, time.Duration(ttl)*time.Second).Err()
}

// BatchSetProducts 批量设置商品缓存（预热）
func (r *redisRepo) BatchSetProducts(ctx context.Context, products []*biz.CachedSeckillProduct) error {
	pipe := r.rdb.Pipeline()
	for _, p := range products {
		key := fmt.Sprintf("seckill:sku:%d", p.SkuID)
		data, _ := json.Marshal(p)
		pipe.SetEX(ctx, key, data, 2*time.Hour)

		stockKey := fmt.Sprintf("seckill:stock:%d", p.SkuID)
		pipe.Set(ctx, stockKey, p.AvailableStock, 0)
	}
	_, err := pipe.Exec(ctx)
	return err
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
