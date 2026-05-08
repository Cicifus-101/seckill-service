package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"seckill-service/internal/observability"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"

	"seckill-service/internal/biz"
)

// acquire
var acquireLockScript = redis.NewScript(`
if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'PX', ARGV[2]) then
    return 1
end
return 0
`)

// renew
var renewLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

// release
var releaseLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`)

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
    redis.call('setex', KEYS[2], ARGV[2], 1)
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

		stockKey := fmt.Sprintf("seckill:act:%d:sku:%d:stock", p.ActivityID, p.SkuID)
		pipe.Set(ctx, stockKey, p.AvailableStock, 0)
		pipe.Do(ctx, "BF.ADD", bloomProductKey(p.ActivityID), fmt.Sprintf("%d", p.ProductID))
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisRepo) GetProductList(ctx context.Context, activityID int64, page, pageSize, sortType int32) (res *biz.SeckillProductsResult, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.GetProductList")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "GetProductList", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	key := fmt.Sprintf("seckill:act:%d:list:%d:%d:%d", activityID, page, pageSize, sortType)

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

func (r *redisRepo) SetProductList(ctx context.Context, activityID int64, page, pageSize, sortType int32, data *biz.SeckillProductsResult, ttl time.Duration) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.SetProductList")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "SetProductList", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	key := fmt.Sprintf("seckill:act:%d:list:%d:%d:%d", activityID, page, pageSize, sortType)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	ttl = addRandomJitter(ttl)
	return r.rdb.SetEX(ctx, key, jsonData, ttl).Err()
}

// GetStock 获取库存
func (r *redisRepo) GetStock(ctx context.Context, activityID, skuID uint64) (int64, error) {
	key := fmt.Sprintf("seckill:act:%d:sku:%d:stock", activityID, skuID)
	return r.rdb.Get(ctx, key).Int64()
}

// SetStock 设置库存
func (r *redisRepo) SetStock(ctx context.Context, activityID, skuID uint64, stock int64) error {
	key := fmt.Sprintf("seckill:act:%d:sku:%d:stock", activityID, skuID)
	return r.rdb.Set(ctx, key, stock, 0).Err()
}

// DeductStock 原子扣减库存
func (r *redisRepo) DeductStock(ctx context.Context, activityID, skuID, userID uint64, quantity int) (res int, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.DeductStock")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "DeductStock", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	stockKey := fmt.Sprintf("seckill:act:%d:sku:%d:stock", activityID, skuID)
	userKey := fmt.Sprintf("seckill:act:%d:sku:%d:buy:%d", activityID, skuID, userID)

	res, err = r.luaScript.Run(ctx, r.rdb,
		[]string{stockKey, userKey},
		quantity, 900,
	).Int()

	return res, err
}

// RollbackStock 回滚库存
func (r *redisRepo) RollbackStock(ctx context.Context, activityID, skuID uint64, quantity int) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.RollbackStock")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "RollbackStock", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	key := fmt.Sprintf("seckill:act:%d:sku:%d:stock", activityID, skuID)
	return r.rdb.IncrBy(ctx, key, int64(quantity)).Err()
}

// CheckUserBuy 检查用户购买
func (r *redisRepo) CheckUserBuy(ctx context.Context, activityID, skuID, userID uint64) (bool, error) {
	key := fmt.Sprintf("seckill:act:%d:sku:%d:buy:%d", activityID, skuID, userID)
	exist, err := r.rdb.Exists(ctx, key).Result()
	return exist == 1, err
}

// MarkUserBuy 标记用户购买
func (r *redisRepo) MarkUserBuy(ctx context.Context, activityID, skuID, userID uint64, ttl int64) error {
	key := fmt.Sprintf("seckill:act:%d:sku:%d:buy:%d", activityID, skuID, userID)
	return r.rdb.SetEX(ctx, key, 1, time.Duration(ttl)*time.Second).Err()
}

// RemoveUserBuy 删除用户购买标记
func (r *redisRepo) RemoveUserBuy(ctx context.Context, activityID, skuID, userID uint64) error {
	key := fmt.Sprintf("seckill:act:%d:sku:%d:buy:%d", activityID, skuID, userID)
	return r.rdb.Del(ctx, key).Err()
}

// GetCurrentActivity 获取当前活动
func (r *redisRepo) GetCurrentActivity(ctx context.Context) (res *biz.Activity, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.GetCurrentActivity")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "GetCurrentActivity", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

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
func (r *redisRepo) SetCurrentActivity(ctx context.Context, activity *biz.Activity, ttl time.Duration) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.SetCurrentActivity")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "SetCurrentActivity", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	key := "seckill:current:activity"
	data, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	return r.rdb.SetEX(ctx, key, data, ttl).Err()
}

// GetProductDetail 获取商品详情缓存
func (r *redisRepo) GetProductDetail(ctx context.Context, productID, activityID uint64) (res *biz.SeckillProductDetail, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.GetProductDetail")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "GetProductDetail", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()
	key := fmt.Sprintf("seckill:act:%d:product:%d:detail", activityID, productID)
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
func (r *redisRepo) SetProductDetail(ctx context.Context, productID, activityID uint64, detail *biz.SeckillProductDetail, ttl time.Duration) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.SetProductDetail")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "SetProductDetail", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	key := fmt.Sprintf("seckill:act:%d:product:%d:detail", activityID, productID)
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	ttl = addRandomJitter(ttl)
	return r.rdb.SetEX(ctx, key, data, ttl).Err()
}

func bloomProductKey(activityID uint64) string {
	return fmt.Sprintf("seckill:bloom:product:%d", activityID)
}
func (r *redisRepo) BloomAdd(ctx context.Context, activityID, productID uint64) error {
	return r.rdb.Do(ctx, "BF.ADD", bloomProductKey(activityID), fmt.Sprintf("%d", productID)).Err()
}

func (r *redisRepo) BloomExists(ctx context.Context, activityID, productID uint64) (bool, error) {
	res, err := r.rdb.Do(ctx, "BF.EXISTS", bloomProductKey(activityID), fmt.Sprintf("%d", productID)).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

func (r *redisRepo) DeleteCoupon(ctx context.Context, couponID uint64) error {
	key := fmt.Sprintf("seckill:coupon:%d", couponID)
	return r.rdb.Del(ctx, key).Err()
}

func pendingReservationKey(requestID string) string {
	return fmt.Sprintf("seckill:pending:%s", requestID)
}

func (r *redisRepo) SetPendingReservation(ctx context.Context, pending *biz.PendingReservation, ttl time.Duration) error {
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return r.rdb.SetEX(ctx, pendingReservationKey(pending.RequestID), data, ttl).Err()
}

func (r *redisRepo) DeletePendingReservation(ctx context.Context, requestID string) error {
	return r.rdb.Del(ctx, pendingReservationKey(requestID)).Err()
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

// AcquireLock 获取分布式锁
func (r *redisRepo) AcquireLock(ctx context.Context, key, token string, ttl time.Duration) (rs bool, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.AcquireLock")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "AcquireLock", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	res, err := acquireLockScript.Run(ctx, r.rdb, []string{key}, token, int(ttl.Milliseconds())).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

// RenewLock 刷新分布式锁
func (r *redisRepo) RenewLock(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	res, err := renewLockScript.Run(ctx, r.rdb, []string{key}, token, int(ttl.Milliseconds())).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

// RenewLock 释放自己的分布式锁（防误删）
func (r *redisRepo) ReleaseLock(ctx context.Context, key, token string) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.redis.ReleaseLock")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("redis", "ReleaseLock", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	_, err = releaseLockScript.Run(ctx, r.rdb, []string{key}, token).Int()
	return err
}
