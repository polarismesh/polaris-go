# examples/route — 北极星（Polaris）路由能力 Demo 集合

本目录集合了 polaris-go SDK **路由 / 泳道 / 就近 / 元数据 / 规则** 等核心治理能力的端到端示例，并提供一键聚合测试脚本与清理脚本，方便在接入新版本或调研某一能力时快速验证链路。

## 目录结构

```
examples/route/
├── lane/                     # 泳道路由（LaneRouter）
│   ├── gateway/ consumer/ provider/ ...
│   ├── lane-test.sh          # 入口测试脚本（用例编号严格递增）
│   ├── test.md               # 用例清单
│   ├── cleanup.sh            # 单 demo 清理
│   └── README.md / README-zh.md
├── metadata/                 # 元数据路由（DstMetaRouter）
│   ├── simple-consumer/ consumer/ provider/
│   ├── verify_metadata_route.sh
│   ├── cleanup.sh
│   └── README.md / README-zh.md
├── nearby/                   # 就近路由（NearbyBasedRouter）
│   ├── simple-consumer/ consumer/ provider/ k8s/ ...
│   ├── verify_nearby_route.sh
│   ├── cleanup.sh
│   └── README.md / README-zh.md
├── rule/                     # 规则路由 + failoverType（RuleBasedRouter）
│   ├── simple-consumer/ consumer/ provider/
│   ├── verify_rule_route.sh
│   ├── cleanup.sh
│   └── README.md / README-zh.md
├── run_all_tests.sh          # 【聚合】一次性跑完 4 个 demo
├── cleanup_all.sh            # 【聚合】一次性清理全部残留
└── README.md                 # 本文件
```

## 4 个路由 Demo 速览

| Demo      | 场景                           | 入口脚本                              | 主要路由插件               |
|-----------|--------------------------------|---------------------------------------|----------------------------|
| lane      | 按请求头染色/泳道隔离          | `lane/lane-test.sh`                   | `laneRouter`               |
| metadata  | 按 Consumer 侧 metadata 严格匹配 | `metadata/verify_metadata_route.sh`   | `dstMetaRouter`            |
| nearby    | 按 Region/Zone/Campus 就近路由 | `nearby/verify_nearby_route.sh`       | `nearbyBasedRouter`        |
| rule      | 按 Query/Header 条件规则路由   | `rule/verify_rule_route.sh`           | `ruleBasedRouter`          |

每个 demo 都独立可跑：

```bash
# 泳道路由
./lane/lane-test.sh all 127.0.0.1

# 元数据路由
./metadata/verify_metadata_route.sh --polaris-server 127.0.0.1

# 就近路由
./nearby/verify_nearby_route.sh --polaris-server 127.0.0.1

# 规则路由 + failoverType=all/none
./rule/verify_rule_route.sh --polaris-server 127.0.0.1
```

子 demo 详细的拓扑、用例编号、验证原理见各子目录下的 `README-zh.md` / `test.md`。

## 前置条件

1. 已运行的北极星服务端（Polaris Server），默认地址 `127.0.0.1:8091`（gRPC） + `127.0.0.1:8090`（HTTP 管控）。
2. 本地安装 **Go 1.15+**、**python3**（脚本依赖 python3 解析/构造 Polaris API JSON）、`curl`、`lsof`。
3. 每个 demo 会自动在北极星服务端注册/创建所需的路由规则（除 lane 需要已配置泳道规则，脚本会校验）。

## 一键聚合测试 — `run_all_tests.sh`

脚本会依次调用 4 个子 demo 的入口脚本，收集每个 demo 的验证结论与退出码，并在末尾输出聚合报告。

### 基本用法

```bash
chmod +x run_all_tests.sh
./run_all_tests.sh                                # 跑全部 4 个 demo
./run_all_tests.sh --polaris-server 10.0.0.1      # 指定北极星地址
./run_all_tests.sh --only rule,nearby             # 只跑 rule 与 nearby
./run_all_tests.sh --skip lane                    # 跳过 lane
./run_all_tests.sh --stop-on-first-failure        # 第一个失败即停止
```

### 关键选项

| 参数                        | 说明                                                         |
|-----------------------------|--------------------------------------------------------------|
| `--polaris-server <地址>`   | 北极星服务端地址（默认 `127.0.0.1`）                         |
| `--polaris-token <令牌>`    | 北极星鉴权令牌                                               |
| `--only <列表>`             | 仅运行指定 demo，逗号分隔：`lane,metadata,nearby,rule`       |
| `--skip <列表>`             | 跳过指定 demo                                                |
| `--continue-on-failure`     | 失败不中断（默认行为）                                       |
| `--stop-on-first-failure`   | 遇到第一个失败立即停止                                       |
| `--debug`                   | 透传 `--debug` 给子脚本（影响 SDK 日志级别，仅部分脚本生效） |

### 判定规则

每个子 demo 跑完后，聚合脚本按如下顺序判断其结论：

1. 子脚本输出包含 `验证结论: ✅ ...` → `PASS`
2. 子脚本输出包含 `验证结论: ❌ ...` → `FAIL`
3. 子脚本输出包含 `验证结论: ⚠️ ...` → `PARTIAL`
4. 子脚本输出包含 `所有泳道路由测试用例通过` → `PASS`（lane 特有）
5. 子脚本输出包含 `有 N 个测试用例失败` → `FAIL`（lane 特有）
6. 以上都没匹配上，按子脚本退出码兜底：`0` → `PASS`，其他 → `FAIL`

### 输出示例

```
╔═══════════════════════════════════════════════════════════════════╗
║                    examples/route 测试结果汇总                   ║
╚═══════════════════════════════════════════════════════════════════╝

  Demo       结论         退出码     耗时(秒)   日志
  ---------- ------------ ---------- ---------- ----------------------------------------
  lane       PASS         0          185        .logs/lane-20260424_170000.log
  metadata   PASS         0          92         .logs/metadata-20260424_170310.log
  nearby     PASS         0          78         .logs/nearby-20260424_170445.log
  rule       PASS         0          143        .logs/rule-20260424_170610.log

  总数(不含 SKIP): 4,  PASS=4,  PARTIAL=0,  FAIL=0,  SKIP=0
  成功率:          100.0%
  聚合日志:        .logs/run_all_tests-20260424_170000.log

╔═══════════════════════════════════════════════════════════════════╗
║  最终结论: ✅ 全部 4 个路由 demo 验证通过！                           ║
╚═══════════════════════════════════════════════════════════════════╝
```

### 退出码

- `0` = 全部 PASS
- `>0` = FAIL 数 + PARTIAL 数之和（便于 CI 快速判定）

### 日志

- 聚合总日志：`examples/route/.logs/run_all_tests-<时间戳>.log`（ANSI 颜色码已剥离）
- 每个子 demo 的独立日志：`examples/route/.logs/<demo>-<时间戳>.log`
- 子 demo 内部的详细日志（Provider/Consumer/CSV 等）仍位于各子目录的 `.logs/` 下

## 一键聚合清理 — `cleanup_all.sh`

脚本会依次调用 4 个子目录下的 `cleanup.sh`，再做一次全局兜底清理（按进程命令行前缀 `examples/route/*/.build/<bin>` 匹配），最后清理聚合脚本自己的 `.logs` 目录。

### 基本用法

```bash
chmod +x cleanup_all.sh
./cleanup_all.sh             # 交互式（子脚本会各自询问确认）
./cleanup_all.sh -f          # 强制清理，不询问
./cleanup_all.sh --dry-run   # 仅展示残留项，不执行清理
./cleanup_all.sh -f --skip lane  # 强制清理但跳过 lane
```

### 清理内容

1. 每个子 demo 的 `cleanup.sh` 清理其本目录：
   - 按进程命令行前缀 `<demo>/.build/...` 匹配并终止残留进程
   - 删除 `.build/` 与 `.logs/` 目录
2. 全局兜底：扫描所有 `examples/route/*/.build/(provider|consumer|simple_consumer|gateway|simple-gateway|simple-consumer)` 进程并终止（防止某个 demo 目录被用户改名后仍有同名进程遗留）
3. 删除聚合脚本的 `examples/route/.logs/`

### 参数

| 参数                  | 说明                                                  |
|-----------------------|-------------------------------------------------------|
| `-f`, `--force`       | 不交互直接清理                                        |
| `--dry-run`           | 仅展示匹配的进程/目录，不执行清理                     |
| `--skip <列表>`       | 跳过指定 demo 的子清理，逗号分隔（`lane,metadata,nearby,rule`） |
| `-h`, `--help`        | 展示帮助                                              |

## 典型工作流

### 完整验证

```bash
cd examples/route
./run_all_tests.sh --polaris-server 127.0.0.1
# 发现问题后：
cat .logs/rule-*.log
# 所有验证完成后：
./cleanup_all.sh -f
```

### 只验证规则路由

```bash
./run_all_tests.sh --only rule
./cleanup_all.sh -f --skip lane,metadata,nearby
```

### CI 集成

```yaml
- name: Run polaris-go route demos
  run: |
    cd examples/route
    chmod +x run_all_tests.sh cleanup_all.sh
    ./run_all_tests.sh --polaris-server ${{ env.POLARIS_HOST }} \
                       --polaris-token ${{ secrets.POLARIS_TOKEN }}
- name: Cleanup
  if: always()
  run: examples/route/cleanup_all.sh -f
```

## 故障排查

| 症状                                   | 可能原因                                                 |
|----------------------------------------|----------------------------------------------------------|
| 聚合脚本立即报 `Go 未安装`             | 未安装 Go 1.15+                                          |
| 聚合脚本报 `python3 未安装`            | 子脚本依赖 python3 解析 Polaris API，请安装              |
| 某 demo 卡在 `等待 HTTP 服务就绪`      | 固定端口被占用，先 `./cleanup_all.sh -f` 再重试           |
| lane 报 `未找到泳道规则`               | 需先在北极星控制台按 `lane/test.md` 配置泳道规则          |
| rule demo 全部 FAIL、路由到随机 Provider | 北极星存量规则是 `type=CUSTOM`；最新版本脚本已改为 `type=QUERY`，清理旧规则后重跑 |
| nearby PASS 但 Consumer 调不到 Provider | Consumer / Provider 地域配置不一致，核对 `--region/--zone/--consumer-campus` |

> 遇到 `bind: address already in use`：统一执行 `./cleanup_all.sh -f` 再重跑。

## 约定与规范

本目录下所有测试/验证脚本遵循以下规约：

1. **用例编号严格顺序递增**：脚本中 `[用例N.M]` 或 `步骤 N/M` 标识必须与执行顺序、文档编号一致。
2. **文档同步**：新增用例须同步更新所在 demo 的 `test.md` / `README.md` / `README-zh.md`。
3. **清理同步**：新增进程/端口/产物须同步更新所在 demo 的 `cleanup.sh`；新增子 demo 还须同步 `run_all_tests.sh` 的 `DEMOS` 列表与 `cleanup_all.sh` 的子清理循环。

## 相关文档

- [lane/test.md](lane/test.md) — 泳道路由详细用例清单
- [metadata/README-zh.md](metadata/README-zh.md) — 元数据路由原理与配置
- [nearby/README-zh.md](nearby/README-zh.md) — 就近路由地域模型
- [rule/README-zh.md](rule/README-zh.md) — 规则路由 + failoverType 说明
- [polaris-go 仓库](https://github.com/polarismesh/polaris-go)
