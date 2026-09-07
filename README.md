# Seckill Service

基于 Go + Kratos 构建的高并发秒杀服务示例，围绕“Redis 预占库存、Kafka 异步下单、MySQL 事务落库、补偿与超时取消”实现秒杀订单主链路，并提供优惠券、支付、死信重放和缓存一致性写入口。

> 本项目是后端架构与高并发场景的工程实践项目，不是可直接用于生产环境的电商系统。鉴权、支付渠道、配置中心和集群部署策略需要根据实际环境补充。

## 核心能力

- HTTP/JSON 与 gRPC 双协议 API，由 Proto 文件统一定义并生成代码
- 秒杀商品、活动、订单、支付、支付回调和秒杀结果查询
- Redis 限流、请求幂等、Bloom Filter 防穿透、分布式锁和库存预占
- Kafka 异步创建订单，支持重试、Retry Topic、DLQ 和死信重放
- MySQL 多库拆分：用户库、商品库、秒杀核心库、支付库
- 延迟队列处理待支付订单超时取消，补偿任务修复悬挂预占、库存和超时订单
- 用户券发放、查询、核销与失败恢复；支持评价场景触发发券
- OpenTelemetry + Jaeger 链路追踪，Prometheus 指标监控
- Docker Compose 一键启动 Redis、Kafka、ZooKeeper、Jaeger、OTel Collector 和 Prometheus

## 技术栈

| 类别 | 技术 |
| --- | --- |
| 语言 | Go 1.25+ |
| Web/RPC | Kratos、gRPC、grpc-gateway/HTTP |
| 数据库 | MySQL、GORM、GORM Gen |
| 缓存 | Redis、RedisBloom |
| 消息 | Kafka、Sarama |
| 可观测性 | OpenTelemetry、Jaeger、Prometheus |
| 依赖注入 | Google Wire |
| 本地环境 | Docker Compose |

## 架构设计

```text
Client ──HTTP/gRPC──▶ API/Service ──▶ Biz/Usecase
                                      │
                      ┌───────────────┼───────────────┐
                      ▼               ▼               ▼
                   MySQL            Redis           Kafka
                 四业务库       缓存/限流/预占       异步下单
                                      │               │
                                      └───────┬───────┘
                                              ▼
                                  Consumer / Jobs
                             事务确认·重试·DLQ·补偿·取消
```

### 一次秒杀下单流程

1. 请求进入 API 层，执行参数校验、限流和请求幂等校验。
2. Redis 原子预扣库存并记录用户购买标记，同时写入预占凭证。
3. 发送 Kafka 订单消息；发送失败时回滚 Redis 库存、购买标记和用户券。
4. Kafka Consumer 在 MySQL 事务中创建订单、写入收货地址快照并扣减数据库库存。
5. 消费失败按策略重试，超过次数进入死信队列；补偿任务负责处理长期悬挂预占。
6. 待支付订单进入 Redis 延迟队列，超时后恢复库存、购买标记和用户券。

项目不依赖跨 Redis、Kafka、MySQL 的分布式事务，而是通过幂等、预占凭证、补偿、重试和死信机制实现最终一致性。

## 目录结构

```text
.
├── api/seckill/v1/       # Proto、生成的 HTTP/gRPC 代码
├── cmd/
│   ├── seckill-service/  # 服务启动入口与 Wire 组装
│   └── gen/              # GORM 模型/查询代码生成
├── configs/              # 本地开发配置
├── docker/               # Prometheus、OTel 和 Docker 配置
├── internal/
│   ├── biz/              # 应用编排、领域规则和业务模型
│   ├── cache/            # 限流、幂等、Redis 防护
│   ├── conf/             # 配置 Proto 与生成代码
│   ├── data/             # MySQL、Redis、事务和 Repository
│   ├── job/              # 延迟队列、选主、补偿任务
│   ├── kafka/            # Producer、Consumer、重试和 DLQ
│   ├── mq/               # 消息模型
│   ├── observability/    # Trace、Metrics、中间件
│   ├── server/           # HTTP/gRPC Server
│   └── service/          # API 到 Biz 的适配层
├── scripts/              # 数据库初始化、表结构和演示数据
├── third_party/          # Proto 编译所需依赖
├── docker-compose.yml     # 本地基础设施编排
├── openapi.yaml           # 生成的 OpenAPI 文档
├── Makefile               # 生成、构建、初始化命令
└── Dockerfile             # 服务镜像构建文件
```

## 环境要求

- Go 1.25 或更高版本
- Docker Desktop 与 Docker Compose
- MySQL 8.x
- GNU Make（Windows 可使用 Git Bash、WSL 或其他 Make 环境）
- `protoc`（仅在重新生成 Proto 代码时需要）

## 快速开始

### 1. 启动基础设施

```bash
docker compose up -d
docker compose ps
```

Compose 会启动 Redis、Kafka、ZooKeeper、Jaeger、OpenTelemetry Collector 和 Prometheus。MySQL 需要单独准备，并按照 `scripts/seckill.sql` 初始化数据库和表。

### 2. 配置服务

默认配置文件为 `configs/config.yaml`，启动时通过 `-conf` 指定配置目录：

```bash
./bin/seckill-service -conf ./configs
```

请在公开仓库使用环境变量、部署密钥或本地未跟踪配置替换数据库和 Redis 凭据；不要把真实密码、Token 或证书提交到 Git。当前仓库中的配置仅适合作为本地演示配置，提交前请检查并替换其中的明文凭据。

### 3. 初始化数据库和生成代码

```bash
# 根据本机环境修改 Makefile 中的 MySQL 连接方式
make init-db
make gen-dao
```

如修改了 Proto 或配置 Proto：

```bash
make init
make api
make config
```

### 4. 构建和运行

```bash
make build
./bin/seckill-service -conf ./configs
```

服务默认监听：

- HTTP：`http://127.0.0.1:8000`
- gRPC：`127.0.0.1:9000`
- Prometheus Metrics：`http://127.0.0.1:2112/metrics`
- Jaeger UI：`http://127.0.0.1:16686`
- Prometheus UI：`http://127.0.0.1:9090`

## API 文档

接口定义位于 [`api/seckill/v1/seckill.proto`](api/seckill/v1/seckill.proto)，生成的 OpenAPI 文档位于 [`openapi.yaml`](openapi.yaml)。主要接口包括：

| 能力 | HTTP 接口 |
| --- | --- |
| 商品列表 | `GET /seckill/products` |
| 商品详情 | `GET /seckill/product/{product_id}` |
| 当前活动 | `GET /api/v1/seckill/activity/current` |
| 秒杀下单 | `POST /seckill/order` |
| 订单查询 | `GET /seckill/order/{order_no}` |
| 秒杀结果 | `GET /api/v1/seckill/result` |
| 支付订单 | `POST /seckill/order/pay` |
| 评价发券 | `POST /api/v1/seckill/coupon/review/grant` |

## 常用命令

```bash
make help       # 查看命令
make build      # 构建服务
make tidy       # 整理 Go 依赖
make api        # 生成 API、HTTP、gRPC 和 OpenAPI 代码
make config     # 生成配置代码
make gen-dao    # 生成 GORM DAO
make all        # 执行全部生成流程
go test ./...   # 运行测试
```

## 设计说明与边界

- Redis 负责高并发入口的快速判定和库存预占，MySQL 是订单与库存的最终持久化来源。
- 活动、商品详情和商品列表采用 Cache Aside；更新时先写数据库，再删除缓存，可选重新预热。
- 秒杀库存采用预热、原子预扣、异步确认、失败回滚和定时补偿，不使用普通缓存删除策略。
- Kafka 采用订单、结果、重试、死信和支付相关 Topic；Topic 初始化配置见 `docker-compose.yml`。
- 补偿任务使用 Redis 选主，避免多实例重复执行同一类后台任务。

## 许可证

本项目采用 [MIT License](LICENSE)。

