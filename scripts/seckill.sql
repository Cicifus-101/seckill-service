-- ======================== 1. 秒杀用户库 ========================
CREATE DATABASE IF NOT EXISTS `seckill_user` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
USE `seckill_user`;

-- 用户表
CREATE TABLE `user` (
                        `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '用户ID',
                        `username` varchar(50) NOT NULL DEFAULT '' COMMENT '用户名',
                        `password` varchar(128) NOT NULL DEFAULT '' COMMENT '用户密码',
                        `phone` varchar(20) NOT NULL DEFAULT '' COMMENT '手机号',
                        `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                        `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                        PRIMARY KEY (`id`),
                        UNIQUE KEY `uniq_phone` (`phone`),
                        UNIQUE KEY `uniq_username` (`username`),
                        KEY `ix_update_time` (`update_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='秒杀用户表';

-- 收货地址表
CREATE TABLE `user_address` (
                                `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
                                `user_id` bigint(20) unsigned NOT NULL DEFAULT '0' COMMENT '用户id',
                                `receiver_name` varchar(64) NOT NULL DEFAULT '' COMMENT '收货人姓名',
                                `receiver_phone` varchar(20) NOT NULL DEFAULT '' COMMENT '收货人手机号',
                                `province` varchar(50) NOT NULL DEFAULT '' COMMENT '省份',
                                `city` varchar(50) NOT NULL DEFAULT '' COMMENT '城市',
                                `district` varchar(50) NOT NULL DEFAULT '' COMMENT '区/县',
                                `detail_address` varchar(200) NOT NULL DEFAULT '' COMMENT '详细地址',
                                `is_default` tinyint(1) unsigned NOT NULL DEFAULT '0' COMMENT '是否默认地址：0-否，1-是',
                                `is_delete` tinyint(1) unsigned NOT NULL DEFAULT '0' COMMENT '是否删除：0-未删，1-已删',
                                `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                                `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                                PRIMARY KEY (`id`),
                                KEY `idx_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户收货地址表';


-- ======================== 2. 秒杀商品库 ========================
CREATE DATABASE IF NOT EXISTS `seckill_product` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
USE `seckill_product`;

-- 商品表
CREATE TABLE `product` (
                           `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '商品id',
                           `name` varchar(200) NOT NULL DEFAULT '' COMMENT '商品名称',
                           `subtitle` varchar(500) NOT NULL DEFAULT '' COMMENT '商品副标题/卖点',
                           `main_image` varchar(500) NOT NULL DEFAULT '' COMMENT '主图地址',
                           `detail` text COMMENT '商品详情',
                           `status` tinyint(4) NOT NULL DEFAULT '1' COMMENT '状态：1-上架，2-下架',
                           `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                           `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                           PRIMARY KEY (`id`),
                           KEY `ix_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='秒杀商品基础表';

-- 商品分类表
CREATE TABLE `category` (
                            `id` smallint(6) unsigned NOT NULL AUTO_INCREMENT COMMENT '分类id',
                            `parent_id` smallint(6) unsigned NOT NULL DEFAULT '0' COMMENT '父分类id，0表示根节点',
                            `name` varchar(50) NOT NULL DEFAULT '' COMMENT '分类名称',
                            `sort_order` tinyint(4) NOT NULL DEFAULT '0' COMMENT '排序值',
    -- 实现的软删除，保存历史数据（已下架的商品可能关联这个表），误操作可以恢复
                            `status` tinyint(4) NOT NULL DEFAULT '1' COMMENT '状态：1-正常，2-废弃',
                            `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                            `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                            PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='商品分类表';


-- ======================== 3. 秒杀核心库 ========================
CREATE DATABASE IF NOT EXISTS `seckill_core` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
USE `seckill_core`;

-- 秒杀活动表
CREATE TABLE `seckill_activity` (
                                    `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '活动ID',
                                    `title` varchar(128) NOT NULL COMMENT '活动标题',
                                    `description` varchar(500) NOT NULL DEFAULT '' COMMENT '活动描述',
                                    `start_time` datetime NOT NULL COMMENT '开始时间',
                                    `end_time` datetime NOT NULL COMMENT '结束时间',
                                    `status` tinyint(4) NOT NULL DEFAULT '0' COMMENT '状态：0-未开始，1-进行中，2-已结束',
                                    `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                                    `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                                    PRIMARY KEY (`id`),
                                    KEY `idx_start_end` (`start_time`, `end_time`),
                                    KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='秒杀活动表';

-- 秒杀商品关联表
CREATE TABLE `seckill_sku` (
                               `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '主键ID',
                               `activity_id` bigint(20) unsigned NOT NULL COMMENT '活动ID',
                               `product_id` bigint(20) unsigned NOT NULL COMMENT '商品ID',
                               `seckill_price` bigint(20) unsigned NOT NULL COMMENT '秒杀价（单位：分）',
                               `market_price` bigint(20) unsigned NOT NULL DEFAULT '0' COMMENT '市场价（单位：分）',
                               `total_stock` int(11) unsigned NOT NULL COMMENT '秒杀总库存',
                               `available_stock` int(11) unsigned NOT NULL COMMENT '剩余可用库存',
                               `limit_num` int(11) unsigned NOT NULL DEFAULT '1' COMMENT '每人限购数量',
                               `version` int(11) unsigned NOT NULL DEFAULT '0' COMMENT '乐观锁版本号',
                               `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                               `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                               PRIMARY KEY (`id`),
                               UNIQUE KEY `uk_activity_product` (`activity_id`, `product_id`) COMMENT '一个活动下商品唯一',
                               KEY `idx_product_id` (`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='秒杀商品表';

-- 优惠券表
CREATE TABLE `coupon` (
                          `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
                          `name` varchar(64) NOT NULL COMMENT '优惠券名称',
                          `type` tinyint(4) NOT NULL COMMENT '类型: 1-满减, 2-打折',
                          `value` bigint(20) unsigned NOT NULL COMMENT '面额（单位：分）或折扣率（如90表示9折）',
                          `min_amount` bigint(20) unsigned NOT NULL COMMENT '使用门槛（单位：分）',
                          `total_count` int(11) unsigned NOT NULL COMMENT '总发行量',
                          `remain_count` int(11) unsigned NOT NULL COMMENT '剩余量',
                          `user_limit` int(11) unsigned NOT NULL DEFAULT '1' COMMENT '每人限领数量',
                          `start_time` datetime NOT NULL COMMENT '开始时间',
                          `end_time` datetime NOT NULL COMMENT '结束时间',
                          `version` int(11) unsigned NOT NULL DEFAULT '0' COMMENT '乐观锁版本号',
                          `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
                          `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                          PRIMARY KEY (`id`),
                          KEY `idx_time` (`start_time`, `end_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='优惠券表';
-- 秒杀订单表
CREATE TABLE `seckill_order` (
                                 `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '订单ID',
                                 `order_no` varchar(32) NOT NULL COMMENT '订单编号',
                                 `user_id` bigint(20) unsigned NOT NULL COMMENT '用户ID',
                                 `activity_id` bigint(20) unsigned NOT NULL COMMENT '活动ID',
                                 `product_id` bigint(20) unsigned NOT NULL COMMENT '商品ID',
    -- 活动+商品+秒杀价格+库存
                                 `sku_id` bigint(20) unsigned NOT NULL COMMENT '秒杀商品关联ID', -- 关联 seckill_sku
                                 `product_name` varchar(200) NOT NULL DEFAULT '' COMMENT '商品名称（快照）',-- 历史订单永远显示下单时的名称
                                 `product_image` varchar(500) NOT NULL DEFAULT '' COMMENT '商品图片（快照）',
                                 `seckill_price` bigint(20) unsigned NOT NULL COMMENT '秒杀单价（快照，单位：分）',
                                 `quantity` int(11) unsigned NOT NULL DEFAULT '1' COMMENT '购买数量',
                                 `order_amount` bigint(20) unsigned NOT NULL COMMENT '订单总金额（单位：分）',
                                 `coupon_id` bigint(20) unsigned DEFAULT NULL COMMENT '使用的优惠券ID',
                                 `address_id` bigint(20) unsigned DEFAULT NULL COMMENT '收货地址ID',
                                 `status` tinyint(4) unsigned NOT NULL DEFAULT '0' COMMENT '状态：0-待支付，1-已支付，2-已取消，3-已退款',
                                 `pay_time` datetime DEFAULT NULL COMMENT '支付时间',
                                 `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                                 `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                                 PRIMARY KEY (`id`),
                                 UNIQUE KEY `uk_order_no` (`order_no`) COMMENT '订单编号唯一',
                                 UNIQUE KEY `uk_user_activity_product` (`user_id`, `activity_id`, `product_id`) COMMENT '一人一单幂等',
                                 KEY `idx_user_id` (`user_id`),
                                 KEY `idx_activity_id` (`activity_id`),
                                 KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='秒杀订单表';

-- 订单收货信息快照表
CREATE TABLE `order_shipping` (
                                  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '主键ID',
                                  `order_no` varchar(32) NOT NULL COMMENT '订单编号',
                                  `receiver_name` varchar(64) NOT NULL DEFAULT '' COMMENT '收货人姓名',
                                  `receiver_phone` varchar(20) NOT NULL DEFAULT '' COMMENT '收货人手机',
                                  `province` varchar(50) NOT NULL DEFAULT '' COMMENT '省份',
                                  `city` varchar(50) NOT NULL DEFAULT '' COMMENT '城市',
                                  `district` varchar(50) NOT NULL DEFAULT '' COMMENT '区/县',
                                  `detail_address` varchar(200) NOT NULL DEFAULT '' COMMENT '详细地址',
                                  `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                                  PRIMARY KEY (`id`),
                                  UNIQUE KEY `uk_order_no` (`order_no`) COMMENT '一个订单对应一个收货信息'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订单收货信息快照表';


-- ======================== 4. 秒杀支付库 ========================
CREATE DATABASE IF NOT EXISTS `seckill_pay` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
USE `seckill_pay`;

-- 支付信息表
CREATE TABLE `pay_info` (
                            `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '支付信息ID',
                            `order_no` varchar(32) NOT NULL COMMENT '订单编号',
                            `user_id` bigint(20) unsigned NOT NULL COMMENT '用户ID',
                            `pay_platform` tinyint(4) NOT NULL COMMENT '支付平台：1-支付宝，2-微信',
                            `platform_number` varchar(128) NOT NULL DEFAULT '' COMMENT '支付平台流水号',
                            `platform_status` varchar(32) NOT NULL DEFAULT '' COMMENT '支付状态',
                            `pay_amount` bigint(20) unsigned NOT NULL COMMENT '支付金额（单位：分）',
                            `pay_time` datetime DEFAULT NULL COMMENT '支付成功时间',
                            `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                            `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                            PRIMARY KEY (`id`),
                            KEY `idx_order_no` (`order_no`),
                            KEY `idx_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='支付信息表';