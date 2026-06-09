# examples/circuitbreaker — 北极星（Polaris）故障熔断 Demo 集合

本目录提供 polaris-go SDK **三种熔断级别**（实例级 / 服务级 / 接口级）的端到端示例，并提供一键 E2E 测试脚本与清理脚本，方便快速验证新版 SDK 是否符合预期。

## 目录结构

```
examples/circuitbreaker/
├── callee/                              # 共用 Provider
│   ├── provider-a/                      #   /echo 默认返回 200
│   └── provider-b/                      #   /echo 默认返回 500
│   说明：两个 provider 都暴露 /switch?openError=true|false 用于运行时切换返回码
│         同时暴露 /order /info /slow 三个端点，配合 newCircuitBreakerCaller 验证多接口熔断
├── newCircuitBreakerCaller/             # 【主推】统一装饰器写法
│   └── consumer/                        #   一份 demo 同时覆盖 SERVICE/METHOD/INSTANCE 三种级别
│                                        #   - MakeFunctionDecorator + RequestContext.Method
│                                        #   - customer func 内 SetInstance 让实例级也走装饰器
├── oldInstanceCircuitBreakerCaller/     # 旧版散装写法（保留兼容）
│   └── consumer/                        #   ConsumerAPI.UpdateServiceCallResult + CircuitBreakerAPI.Report
├── verify_circuitbreaker.sh             # 【主入口】一键 E2E 测试脚本
├── cleanup.sh                           # 进程与目录清理脚本
├── test.md                              # 用例清单
└── README.md                            # 本文件
```

## 三种熔断级别速览

| 级别       | proto Level     | 资源标识                          | 触发后行为                                |
|------------|------------------|-----------------------------------|--------------------------------------------|
| 实例级     | `Level_INSTANCE` | `host:port + service + caller`    | 单实例被摘除，流量转移到其他健康实例       |
| 服务级     | `Level_SERVICE`  | `service + caller`                | 整个服务被熔断，请求返回 `call aborted`    |
| 接口级     | `Level_METHOD`   | `service + protocol/method/path`  | 仅特定接口被熔断，其他接口不受影响         |

> **优先级**：服务级 > 接口级 > 实例级。`CircuitBreakerAPI.Check` 在 `DefaultInvokeHandler` 中按 SERVICE → METHOD 顺序短路。
>
> 实例级熔断的状态会写回 `Instance.CircuitBreakerStatus`，可通过 `GetInstances()` 看到；服务级 / 接口级状态不写回 Instance。

## 半开配额精确管理（HalfOpenStatus）

当资源熔断打开后会经过 `sleepWindow` 才进入半开态。半开态下放行请求受**精确配额**控制：

- **发放配额**：`HalfOpenStatus.AcquirePermission` 用 atomic 计数，并发请求按 `RecoverCondition.ConsecutiveSuccess` 数量逐次放行；
- **配额耗尽**：超额请求会被 SDK 直接拦截返回 `*model.CallAborted`，对应 demo 中 `writeResult` 的 `abort != nil` 分支；
- **归集判定**：所有放行结果归集后，全部成功 → 切回 Close；任一失败 → 立即重新 Open；
- **demo 行为**：`abort.HasFallback() == false` 时（半开配额耗尽路径默认无 fallback），demo 统一返回 `503 + "circuit breaker open: ..."` 文案，便于通过 curl/脚本观察拒绝路径。

## 块级独立错误条件（BlockConfig）

一条规则中的多个 `BlockConfig`（块）相互**独立计数**：

- 每个块持有自己的 `ErrorConditions`：块自身非空时使用块的；为空则回退到规则顶层；都空则透传 `stat.RetStatus`；
- 每个块按 `BlockConfig × TriggerCondition` 笛卡尔积建立独立 trigger counter；
- 任一块的任一 trigger 触发即让整个资源进入 Open——块**不维护**自己的状态机，资源级状态机统一调度。

## 4xx vs 5xx：两条独立的判错链路

demo 与 SDK 之间存在两条**完全独立**的判错链路，处理 HTTP 状态码时必须分开看：

| 链路 | 入口 | 谁来决定"是否失败" | 4xx 行为 |
|---|---|---|---|
| **熔断器** | `customCodeConvert` → `commonReport` → `BlockConfig.ErrorConditions` | **完全由你配置的规则决定**（`RANGE 500~599` / `EXACT` / `IN/NOT_IN` / `REGEX` / `DELAY`） | 默认规则配 `500~599` 时不计入；用户可显式配 `IN [400,500]` 把 4xx 也纳入 |
| **LB / 健康检查** | `ConsumerAPI.UpdateServiceCallResult` 的 `RetStatus` | **demo 自己决定**（影响 weightedrandom 等 LB 插件的实例权重 + 健康检查切换） | demo 默认**只把 5xx 标 `RetFail`**；4xx 视为成功（4xx 通常是请求侧参数 / 鉴权问题，不应摘除实例） |

**约定**：

1. demo 中 `customCodeConvert.OnSuccess` 透传 HTTP 状态码字符串给 SDK，让规则决定是否纳入熔断；
2. demo 中 `customCodeConvert.OnError` 返回 `"-1"` 这类**明显非 HTTP 状态码**的值，避免被 `RANGE 500~599` 等规则误命中或漏命中；
3. demo 中 `reportServiceCallResult` 只在 `StatusCode >= 500` 时标 `RetFail`，4xx 一律 `RetSuccess`；
4. 验证脚本 `verify_circuitbreaker.sh` 默认规则下发的 `ErrorCondition` 同样是 `RANGE 500~599`，与 demo 行为对齐。

## 前置条件

1. 已运行的北极星服务端（Polaris Server）：
   - gRPC: `<polaris-server>:8091`
   - HTTP 管控: `<polaris-server>:8090`
2. 本地安装 **Go**（与 `examples/circuitbreaker/*/go.mod` 兼容版本）、**python3**、`curl`、`lsof`
3. 子目录的 `go.mod` 已通过 `replace github.com/polarismesh/polaris-go => ../../../../` 指向本地源码，因此 `verify_circuitbreaker.sh` 编译时使用的就是当前仓库的 SDK 改动

## 一键 E2E 测试 — `verify_circuitbreaker.sh`

脚本会：
1. 编译 `provider-a` / `provider-b` 与三种 consumer
2. 启动 Provider 集群（注册到 `CircuitBreakerCallee` 服务）
3. 通过 Polaris HTTP API 自动创建 `Level=INSTANCE/SERVICE/METHOD` 三条熔断规则
4. 启动对应 consumer，并发请求触发熔断
5. 通过 `/switch` 翻转 provider 状态验证恢复链路
6. 退出时自动删除所创建的熔断规则并清理进程

### 基本用法

```bash
chmod +x verify_circuitbreaker.sh
./verify_circuitbreaker.sh                                              # 跑全部 6 个用例
./verify_circuitbreaker.sh --polaris-server 10.0.0.1                    # 指定北极星地址
./verify_circuitbreaker.sh --only instance                              # 仅跑实例级
./verify_circuitbreaker.sh --only service,interface,http_status         # 多个用例组合
./verify_circuitbreaker.sh --only default_rule                          # 仅跑默认规则兜底用例
```

### 关键选项

| 参数                        | 说明                                                               |
|-----------------------------|--------------------------------------------------------------------|
| `--polaris-server <地址>`   | 北极星服务端地址（默认 `127.0.0.1`）                               |
| `--polaris-token <令牌>`    | 北极星鉴权令牌                                                     |
| `--namespace <ns>`          | 命名空间（默认 `default`）                                         |
| `--service <name>`          | 被调服务名（默认 `CircuitBreakerCallee`）                          |
| `--only <列表>`             | 仅运行指定用例：`instance`, `service`, `interface`, `old_instance`, `http_status`, `default_rule` |
| `--trigger-count <次数>`    | 触发阶段请求次数（默认 15，越大越容易触发熔断）                    |
| `--recovery-count <次数>`   | 验证 / 恢复阶段请求次数（默认 10）                                 |
| `--wait-half-open <秒数>`   | 服务级用例等待 sleepWindow 进入半开的秒数（默认 35）               |
| `--debug`                   | 透传 `--debug=true` 给所有 provider/consumer 子进程，开启 Polaris SDK DEBUG 日志；demo 自身日志默认携带 `文件:行号` |

### 输出示例

```
╔════════════════════════════════════════════════════════════╗
║   故障熔断验证结果汇总                                    ║
╚════════════════════════════════════════════════════════════╝

  用例         结果
  ------------ ----------
  instance     PASS
  service      PASS
  interface    PASS

╔════════════════════════════════════════════════════════════╗
║  验证结论: ✅ 全部熔断用例通过                            ║
╚════════════════════════════════════════════════════════════╝
```

### 退出码

- `0` = 全部 PASS
- `>0` = FAIL 数量（便于 CI 快速判定）

### 日志位置

- 主日志：`examples/circuitbreaker/.logs/verify_circuitbreaker-<时间戳>.log`
- Provider：`examples/circuitbreaker/.logs/provider_a.log` / `provider_b.log`
- Consumer：`examples/circuitbreaker/.logs/instance_consumer.log` 等

## 一键清理 — `cleanup.sh`

```bash
chmod +x cleanup.sh
./cleanup.sh             # 交互式（先展示再确认）
./cleanup.sh -f          # 强制清理，不询问
./cleanup.sh --dry-run   # 仅展示残留项，不执行清理
```

清理范围：
1. 按命令行匹配 `examples/circuitbreaker/.build/(provider_a|provider_b|*_consumer)` 的进程
2. 删除 `.build/` 与 `.logs/` 目录（询问后）

> 熔断规则在 `verify_circuitbreaker.sh` 退出时已自动删除，`cleanup.sh` 不会再删一次规则。
> 若脚本被 `kill -9` 强制终止导致规则未清理，可手动登录北极星控制台搜索 `cb-instance-*` / `cb-service-*` / `cb-interface-*` 删除。

## 用例细节

详见 [test.md](test.md)。

## 子 demo 独立运行

每个子目录也可独立运行（参考各子目录的 `README.md`）：

```bash
# 统一装饰器写法（推荐，一份 demo 同时覆盖 SERVICE/METHOD/INSTANCE 三种级别）
cd callee/provider-a && make run                    # 一个终端
cd callee/provider-b && make run                    # 另一个终端
cd newCircuitBreakerCaller/consumer && make run     # 第三个终端
# 在控制台手工创建熔断规则后（任意级别），curl http://127.0.0.1:18080/<echo|order|info|slow> 即可观察熔断行为

# 旧版散装写法（保留兼容；存量客户使用 CircuitBreakerAPI.Check / Report
# + ConsumerAPI.UpdateServiceCallResult 时的代码示范）
cd oldInstanceCircuitBreakerCaller/consumer && make run
```

## 故障排查

| 症状 | 可能原因 |
|------|---------|
| `Go 未安装` / `python3 未安装` | 安装对应工具 |
| `bind: address already in use` | 端口占用，先 `./cleanup.sh -f` 再重试 |
| 触发阶段一直返回 200 | provider-b 的 `/switch?openError=true` 没生效，或 SDK 未拉取到规则；查看 `.logs/*_consumer.log` |
| 服务级用例的 `恢复阶段` 失败 | sleepWindow 时间不够，可加大 `--wait-half-open` |
| 接口级用例不熔断 | 检查 SDK 是否已应用本仓库的 metadata 改造（`MethodResource.Path` 字段、`matchMethodWithAPI`、`BlockConfig` 处理） |
| 创建规则报 `code != 200000/200001` | 北极星鉴权未通过、规则字段不合法或服务不存在；查看脚本输出的 HTTP 响应 |
| 创建的规则未被删掉 | 脚本被 `kill -9` 强杀；登录控制台搜索 `cb-instance-*`/`cb-service-*`/`cb-interface-*` 手工删除 |

## CI 集成示例

```yaml
- name: Run polaris-go circuitbreaker E2E
  run: |
    cd examples/circuitbreaker
    chmod +x verify_circuitbreaker.sh cleanup.sh
    ./verify_circuitbreaker.sh --polaris-server ${{ env.POLARIS_HOST }} \
                               --polaris-token ${{ secrets.POLARIS_TOKEN }}
- name: Cleanup
  if: always()
  run: examples/circuitbreaker/cleanup.sh -f
```

## 约定与规范

参考 `examples/route/README.md` 章节"约定与规范"：
1. **用例编号严格顺序递增**：脚本中 `用例N.M` 标识与文档编号一致
2. **文档同步**：新增用例须同步更新 `test.md`
3. **清理同步**：新增进程/端口/产物须同步更新 `cleanup.sh`
