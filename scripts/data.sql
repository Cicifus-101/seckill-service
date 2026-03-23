-- ======================== 1. 清理用户库 ========================
USE `seckill_user`;
-- 清空用户和地址，防止压测时用户 ID 冲突
TRUNCATE TABLE `user`;
TRUNCATE TABLE `user_address`;

-- ======================== 2. 清理商品库 ========================
USE `seckill_product`;
-- 清空商品和分类数据
TRUNCATE TABLE `product`;
TRUNCATE TABLE `category`;

-- ======================== 3. 清理核心库 ========================
USE `seckill_core`;
-- 必须清空订单和发货表，否则无法重复使用相同的订单号或进行同一用户第二次秒杀
TRUNCATE TABLE `seckill_order`;
TRUNCATE TABLE `order_shipping`;

-- 清理活动、SKU 和优惠券，以便重新初始化库存和版本号
TRUNCATE TABLE `seckill_activity`;
TRUNCATE TABLE `seckill_sku`;
TRUNCATE TABLE `coupon`;

-- ======================== 4. 清理支付库 ========================
USE `seckill_pay`;
-- 清空支付流水记录，以支持全新的支付测试流
TRUNCATE TABLE `pay_info`;

-- 切换到用户库
-- 切换到用户库
USE `seckill_user`;

-- 创建存储过程生成用户
DELIMITER $$
DROP PROCEDURE IF EXISTS generate_users$$
CREATE PROCEDURE generate_users()
BEGIN
    DECLARE i INT DEFAULT 1;
    WHILE i <= 500 DO
        INSERT INTO `user` (`id`, `username`, `password`, `phone`)
        VALUES (
            i,
            CONCAT('test_user_', i),
            '$2a$10$NkI8qZqYqYqYqYqYqYqYu',
            CONCAT('138', LPAD(i, 8, '0'))
        )
        ON DUPLICATE KEY UPDATE
                                                   username = VALUES(username),
                                                   phone = VALUES(phone);
SET i = i + 1;
END WHILE;
END$$
DELIMITER ;

-- 执行存储过程
CALL generate_users();

-- 删除存储过程
DROP PROCEDURE generate_users;

-- 创建存储过程生成地址
DELIMITER $$
DROP PROCEDURE IF EXISTS generate_addresses$$
CREATE PROCEDURE generate_addresses()
BEGIN
    DECLARE i INT DEFAULT 1;
    WHILE i <= 500 DO
        INSERT INTO `user_address` (`id`, `user_id`, `receiver_name`, `receiver_phone`, `province`, `city`, `district`, `detail_address`, `is_default`, `is_delete`)
        VALUES (
            i,
            i,
            CONCAT('测试用户', i),
            CONCAT('138', LPAD(i, 8, '0')),
            '广东省',
            '深圳市',
            '南山区',
            CONCAT('科技园', i, '号'),
            1,
            0
        )
        ON DUPLICATE KEY UPDATE
                                                   receiver_name = VALUES(receiver_name),
                                                   receiver_phone = VALUES(receiver_phone),
                                                   detail_address = VALUES(detail_address);
SET i = i + 1;
END WHILE;
END$$
DELIMITER ;

-- 执行存储过程
CALL generate_addresses();

-- 删除存储过程
DROP PROCEDURE generate_addresses;

USE `seckill_product`;
-- 商品表
INSERT INTO `product` (`id`, `name`, `subtitle`, `main_image`, `detail`, `status`) VALUES
    (1, 'iPhone 15 Pro', '苹果旗舰手机', 'https://example.com/iphone.jpg', '详细描述...', 1);

-- 商品分类表
INSERT INTO `category` (`id`, `parent_id`, `name`, `sort_order`, `status`) VALUES
    (1, 0, '手机数码', 1, 1);

USE `seckill_core`;
-- 秒杀活动表
INSERT INTO `seckill_activity` (`id`, `title`, `description`, `start_time`, `end_time`, `status`) VALUES
    (1, '双11秒杀活动', '年度大促', DATE_SUB(NOW(), INTERVAL 1 HOUR), DATE_ADD(NOW(), INTERVAL 23 HOUR), 1);

-- 秒杀商品 SKU
INSERT INTO `seckill_sku` (`id`, `activity_id`, `product_id`, `seckill_price`, `market_price`, `total_stock`, `available_stock`, `limit_num`, `version`) VALUES
    (1001, 1, 1, 500000, 999900, 1000, 1000, 2, 0);

-- 优惠券表
INSERT INTO `coupon` (`id`, `name`, `type`, `value`, `min_amount`, `total_count`, `remain_count`, `user_limit`, `start_time`, `end_time`, `version`, `create_time`, `update_time`) VALUES
                                                                                                                                                                                       (1, '满100减10', 1, 1000, 10000, 500, 500, 1, DATE_SUB(NOW(), INTERVAL 1 HOUR), DATE_ADD(NOW(), INTERVAL 30 DAY), 0, NOW(), NOW()),
                                                                                                                                                                                       (2, '满200减30', 1, 3000, 20000, 300, 300, 1, DATE_SUB(NOW(), INTERVAL 1 HOUR), DATE_ADD(NOW(), INTERVAL 30 DAY), 0, NOW(), NOW()),
                                                                                                                                                                                       (3, '9折优惠券', 2, 90, 0, 1000, 1000, 2, DATE_SUB(NOW(), INTERVAL 1 HOUR), DATE_ADD(NOW(), INTERVAL 30 DAY), 0, NOW(), NOW());