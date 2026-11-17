# AI Registry 云端验证：打包、运行与日志回传

> 在仓库内从“本地打包”开始；解压部署包后从“云端运行”开始。

本目录把 `examples/ai-registry` 的 27 项真实服务端验证打包成可在云端 Linux
测试机直接运行的部署包。云端机器不需要 Go 工具链，只需要能访问 Polaris 的
8090/8091/8093/8094 端口。

整体流程：

```text
本地 build-cloud-package.sh
        -> dist/ai-registry-cloud-*.tar.gz
        -> scp 到云端
云端 run-cloud.sh
        -> 执行 verify
        -> .logs/<run-id>/ 保存 result/state/Provider/SDK 日志
        -> 自动生成 analysis.md / analysis.json
        -> 自动调用 pack-cloud-logs.sh
        -> ai-registry-logs-<run-id>-<时间>.tar.gz
        -> scp 回本地
本地 import-logs.sh
        -> 校验 SHA256、安全解压、重新生成分析
```

## 前置条件

本地打包机：

- Go 工具链和 Bash
- 能执行交叉编译：默认 `GOOS=linux GOARCH=amd64`
- 云端目标为 arm64 时使用 `--goarch arm64`

云端测试机：

- Linux amd64/arm64，Bash、`tar`、`sha256sum` 或 `shasum`
- 能访问服务端 Discover `8091`、Console `8090`、Config `8093`、Skill `8094`
- 如使用 HTTPS Console，需要系统 CA 或云端已安装对应 CA

云端不需要 Go、仓库源码或本地 `.build/.logs`。

## 1. 本地打包

在仓库根目录执行：

```bash
bash examples/ai-registry/cloud/build-cloud-package.sh
```

默认生成：

```text
examples/ai-registry/dist/ai-registry-cloud-<时间>-linux-amd64-<pid>.tar.gz
examples/ai-registry/dist/....tar.gz.sha256
```

指定架构或输出路径：

```bash
bash examples/ai-registry/cloud/build-cloud-package.sh \
  --goarch arm64 \
  --output /tmp/ai-registry-cloud.tar.gz
```

清理本地 dist：

```bash
bash examples/ai-registry/cloud/build-cloud-package.sh --clean
```

包内包含：

| 文件 | 用途 |
| --- | --- |
| `polaris-go-ai-registry` | Linux 目标架构 E2E 二进制，静态交叉编译 |
| `ai-registry-summarize` | 结果摘要器，生成 Markdown/JSON 分析 |
| `run-cloud.sh` | 云端运行入口，自动执行验证和日志打包 |
| `pack-cloud-logs.sh` | 云端日志打包脚本，可单独重复使用 |
| `import-logs.sh` | 本地安全导入、校验和解压日志包 |
| `package.env` | 构建时间、Git revision、目标架构、二进制 SHA256 |
| `README.md` | 本说明 |
| `E2E-CASES.md` | 从 `test.md` 复制的 27 项用例参考 |
| `SHA256SUMS` | 包内文件校验和 |

包内**不包含** token、`polaris.yaml`、`--config-file` 指向的本地配置或仓库源码。
`package.env` 的 `GIT_REVISION` 若带 `-dirty`，表示二进制来自包含未提交改动的当前工作树。

## 2. 上传到云端

```bash
scp examples/ai-registry/dist/ai-registry-cloud-*.tar.gz \
  <user>@<cloud-host>:/tmp/
```

云端解压：

```bash
cd /tmp
tar xzf ai-registry-cloud-*.tar.gz
cd ai-registry-cloud-*
```

可选校验：

```bash
sha256sum -c ai-registry-cloud-*.tar.gz.sha256
(cd ai-registry-cloud-* && sha256sum -c SHA256SUMS)
```

## 3. 云端运行

最小命令：

```bash
./run-cloud.sh --server <polaris-host>
```

推荐远端预算：

```bash
./run-cloud.sh \
  --server 10.0.0.1 \
  --suite all \
  --timeout 2m \
  --run-timeout 35m
```

默认值：

| 参数 | 默认 |
| --- | --- |
| `--suite` | `all` |
| `--namespace` | `go-sdk-integration` |
| `--timeout` | `2m` |
| `--run-timeout` | `35m` |
| 资源清理 | 不清理，保留 Provider、服务和 Skill |

只跑某一组：

```bash
./run-cloud.sh --server 10.0.0.1 --suite agent
./run-cloud.sh --server 10.0.0.1 --suite mcp
./run-cloud.sh --server 10.0.0.1 --suite skill
```

非标准端口可通过 `--` 透传二进制参数，例如：

```bash
./run-cloud.sh --server 10.0.0.1 -- \
  -discover 10.0.0.1:18091 \
  -config-address 10.0.0.1:18093 \
  -skill 10.0.0.1:18094 \
  -console http://10.0.0.1:18090
```

Token 只能通过环境变量传入，不写入 `run.env` 或日志：

```bash
export POLARIS_TOKEN='***'
./run-cloud.sh --server 10.0.0.1
```

可用变量：

- Console：`POLARIS_CONSOLE_TOKEN`、`POLARIS_TOKEN`、`POLARIS_E2E_SERVICE_TOKEN`、`POLARIS_SERVICE_TOKEN`
- Skill：`POLARIS_SKILL_TOKEN`、`POLARIS_TOKEN`
- Config：`POLARIS_CONFIG_TOKEN`、`POLARIS_TOKEN`
- Provider：`POLARIS_E2E_SERVICE_TOKEN`、`POLARIS_SERVICE_TOKEN`

如使用本地 SDK 配置，单独上传文件后传入：

```bash
chmod 600 /tmp/polaris-e2e.yaml
./run-cloud.sh --server 10.0.0.1 --config-file /tmp/polaris-e2e.yaml
```

不要把带 token 的配置打进部署包，也不要把它放进 `.logs/<run-id>/`；日志打包脚本
只收集运行目录和诊断信息，不复制 `--config-file`。

## 4. 云端日志包

`run-cloud.sh` 无论验证成功还是失败，都会尝试调用 `pack-cloud-logs.sh`。

运行目录：

```text
.logs/<run-id>/
├── run-cloud.log          # 云端脚本输出
├── run.env                # 参数、开始/结束时间、退出码
├── package.env            # 部署包构建信息
├── result.json            # 27 项结果和最终 error
├── analysis.md            # 通过/失败、失败项、资源与清理摘要
├── analysis.json          # 机器可读分析
├── state.json             # 服务/Provider/Skill 归属和清理依据
├── *-provider.log         # Provider stdout/stderr
├── *-ready.json           # Provider 业务地址和实例信息
├── polaris/               # SDK 日志
└── skill-persistence/     # Skill 下载/安装缓存
```

自动生成的日志包默认位于：

```text
.logs/ai-registry-logs-<run-id>-<时间>.tar.gz
.logs/....tar.gz.sha256
```

包内结构：

```text
ai-registry-logs-<run-id>-<时间>/
├── <run-id>/              # 完整运行目录
├── diagnostics/
│   ├── context.txt        # 打包主机、日志根目录、run 列表
│   ├── package.env        # 部署包版本
│   ├── runtime.txt        # uname/date/磁盘
│   ├── memory.txt         # 可用时记录 free -m
│   └── processes.txt      # 仍存活的 ai-registry 进程
└── archive-contents.txt
```

手动重复打包一次运行：

```bash
./pack-cloud-logs.sh --run-dir .logs/<run-id>
```

不指定 `--run-dir` 时，优先选择含有 `state.json` 或 `result.json` 的最新目录。

汇总所有运行：

```bash
./pack-cloud-logs.sh --all
```

指定输出：

```bash
./pack-cloud-logs.sh \
  --run-dir .logs/<run-id> \
  --output /tmp/ai-registry-logs.tar.gz
```

日志包只支持 `.tar.gz`。脚本会生成 SHA256 文件和 `scp` 提示。

## 5. 回传与本地分析

云端运行结束会打印：

```text
传回本地: scp '<user>@<cloud-host>:/tmp/ai-registry-cloud-.../.logs/ai-registry-logs-....tar.gz*' .
```

本地执行对应 `scp`，然后使用导入脚本：

```bash
scp '<user>@<cloud-host>:/tmp/ai-registry-cloud-.../.logs/ai-registry-logs-*.tar.gz*' .
bash examples/ai-registry/cloud/import-logs.sh ai-registry-logs-*.tar.gz
```

导入脚本会：

- 校验相邻 `.sha256`；
- 拒绝绝对路径、`..`、符号链接、硬链接和设备文件；
- 解压到 `examples/ai-registry/.logs/imported/<archive-name>/`；
- 对每个 `result.json` 重新生成 `analysis.md` 和 `analysis.json`。

`--force` 只允许替换此前由导入脚本生成且带导入标记的目录，避免误删普通目录。

需要失败 run 让导入命令返回非零时加 `--strict`：

```bash
bash examples/ai-registry/cloud/import-logs.sh --strict ai-registry-logs-*.tar.gz
```

优先查看：

```bash
# 汇总结论
sed -n '1,160p' <archive-name>/<run-id>/analysis.md

# 27 项结果和最终错误
jq . <archive-name>/<run-id>/result.json

# 服务、Provider、Skill 归属；清理时使用
jq . <archive-name>/<run-id>/state.json

# 云端入口日志和最终 PASS/FAIL
tail -100 <archive-name>/<run-id>/run-cloud.log

# 某个 Provider 的具体错误
rg -n 'ERROR|FAIL|timeout|DeadlineExceeded' <archive-name>/<run-id>/*.log
```

如果没有 `jq`，可直接打开 JSON 文件。分析时区分：

- `result.json.results[]`：逐项 PASS/FAIL 和耗时
- `analysis.md` / `analysis.json`：结果统计、失败项、资源和清理状态
- `result.json.error`：最终汇总错误
- `state.json`：云端留下的服务、Provider PID、配置组和 Skill fixture
- `run.env`：本次命令、超时、清理选项、退出码
- `diagnostics/`：云主机、进程和包版本，便于判断环境问题

## 6. 清理

默认不清理，便于回看云端现场。

显式停止本 run 的 Provider 并删除本 run 的服务/实例：

```bash
./run-cloud.sh --server <polaris-host> --cleanup
```

同时删除示例持有的三个 Skill fixture：

```bash
./run-cloud.sh --server <polaris-host> --cleanup --cleanup-skills
```

清理仍按 `state.json` 中的 owner/run 校验，`--cleanup` 只影响本次 run。

## 7. 故障排查

| 现象 | 处理 |
| --- | --- |
| `exec format error` | 目标架构不匹配；用 `--goarch arm64` 或 `amd64` 重新打包 |
| 云端提示没有 `go` | 云端不需要 Go；使用本地生成的包，不要上传源码后重新编译 |
| `provide --server` | 补 `--server`，或设置 `POLARIS_SERVER` |
| Provider ready 超时 | 先看 `run-cloud.log` 和 `*-provider.log`，再检查云端到 8091/8090/8093/8094 的连通性 |
| Console 鉴权失败 | 通过环境变量传 token，或单独上传 600 权限的 `--config-file` |
| 没有日志包 | 检查 `.logs/<run-id>/` 是否存在，手动执行 `./pack-cloud-logs.sh --run-dir ...` |
| `total=1 size=1` 等结果失败 | 先确认 `result.json` 和 Provider 日志；这是验证结果，不要用切换 mock 或跳过用例处理 |

## 8. 与本地验证的关系

云端脚本不改变 `test.md` 中的 27 项用例、断言、命名空间隔离和清理归属规则。
它把编排从本地 `verify_e2e.sh` 搬到云端，并增加：

- Linux 部署包和版本清单
- 云端运行包装器
- 统一 Markdown/JSON 摘要
- 失败也执行的日志归档
- 本地安全导入、校验和诊断信息回传

本地仍可直接使用：

```bash
bash examples/ai-registry/verify_e2e.sh --server <polaris-host>
```
