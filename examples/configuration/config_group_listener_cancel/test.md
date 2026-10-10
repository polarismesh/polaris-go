# 配置分组监听取消：脚本验证流程

本文对应当前目录的 `verify.sh` 和 Go 验证程序，说明如何运行、各阶段实际执行什么、通过条件是什么，以及如何从日志区分监听器问题和环境问题。

快速使用方式见 [README.md](./README.md)。本文侧重验证过程和断言依据。

## 1. 验证目标与代码入口

这套 E2E 验证 `model.CancelableConfigGroupListener` 在缓存生命周期中的行为：缓存存活时正常收到配置组变更，停止后摘除自己的监听器，并释放方法值回调对旧缓存对象的引用，同时保持 SDK 配置组及其他监听器正常工作。

相关文件分工如下：

- [verify.sh](./verify.sh)：构建带 race 检测的程序、传递参数、保存运行日志和传播失败状态。
- [cleanup.sh](./cleanup.sh)：预览并清理本示例进程、构建产物、验证日志和 SDK 缓存。
- [Makefile](./Makefile)：提供 `build`、`build-x86`、`test`、`e2e`、`run`、`clean` 入口。
- [main.go](./main.go)：解析参数、校验地址、处理终止信号，输出最终 PASS 或 FAIL。
- [fixtures.go](./fixtures.go)：创建和清理临时配置数据，校验 HTTP 响应，提供带超时的等待函数。
- [scenarios.go](./scenarios.go)：创建真实 SDK，发布配置，执行五个场景并检查结果。
- [access_cache.go](./access_cache.go)：模拟 access 的 `Run / WarmUp / Fetch / Stop / Cleanup / MarkStopping` 生命周期，维护回调统计和弱引用。
- [e2e_test.go](./e2e_test.go)：通过 `go test` 调用同一套 `runE2E` 流程。

配置的创建、更新、发布、拉取，以及 SDK 分组缓存和回调分发，都走真实 Polaris 链路。`resourceCache` 和 `configGroupFetcher` 是生命周期模型，没有运行实际 access 服务，也没有实现其索引转换、HTTP 请求处理或 long polling 业务逻辑。

## 2. 运行方式与参数

### 2.1 环境要求

在仓库根目录进入示例目录：

```bash
cd examples/configuration/config_group_listener_cancel
```

需要准备：

- Go 1.24 或更高版本；示例使用 `weak.Pointer` 检查对象回收。
- Bash 和支持本机 race 构建的 C 编译环境。
- 可访问的 Polaris 发现 gRPC、配置 gRPC 和管理 HTTP 服务。
- 一个已存在的测试命名空间，默认使用 `default`。
- 创建、更新、发布、读取和删除测试配置所需的权限；启用鉴权时提前设置环境变量 `POLARIS_TOKEN`。

示例的 `go.mod` 使用 `replace github.com/polarismesh/polaris-go => ../../..`，因此构建和验证的是当前工作区 SDK。

### 2.2 使用示例地址

文档统一使用 `127.0.0.1` 作为示例地址，运行时请按实际环境替换。下面的命令显式指定三个地址，避免已有环境变量指向其他服务：

```bash
bash verify.sh \
  -server 127.0.0.1 \
  -discovery-addr 127.0.0.1:8091 \
  -config-addr 127.0.0.1:8093 \
  -openapi http://127.0.0.1:8090
```

未设置三个独立地址的环境变量时，可以简写为：

```bash
bash verify.sh -server 127.0.0.1
```

`-server` 只补齐尚未设置的地址，默认使用 `8091 / 8093 / 8090`。独立地址先读取 `POLARIS_DISCOVERY_ADDR`、`POLARIS_CONFIG_ADDR`、`POLARIS_OPENAPI`，对应命令行参数可覆盖环境变量。

也可以通过 Makefile 运行：

```bash
make e2e SERVER=127.0.0.1
make e2e SERVER=127.0.0.1 ARGS='-namespace default -rounds 5 -fetchers 4'
```

`make run` 和 `make e2e` 执行相同的验证流程。`GO` 可以指定 Go 可执行文件，例如：

```bash
GO=/path/to/go/bin/go bash verify.sh -server 127.0.0.1
```

应确保该可执行文件和 `GOROOT` 属于同一工具链。若出现 `compile: version ... does not match go tool version ...`，程序尚未进入服务端验证阶段。

### 2.3 默认参数及作用

- `-namespace default`：已有测试命名空间；程序不会创建命名空间。
- `-listener-mode cancelable`：缓存使用可取消接口；`legacy` 用于旧接口泄漏对照。
- `-rounds 20`：TTL 场景中创建下一代并淘汰旧一代的轮数。
- `-fetchers 8`：TTL 场景每代缓存对象的数量；前面三个场景各使用一个缓存。
- `-ttl 100ms`：TTL 场景中用于触发旧一代 Stop 的定时器时长。
- `-stop-timeout 100ms`：单个缓存 Stop 等待后台工作结束的上限。
- `-timeout 20s`：文件发布确认、事件到达、取消完成、对象回收等步骤使用的等待超时。
- `-run-timeout 5m`：传入整套场景的 context 超时。
- `-debug`：开启 SDK debug 日志，默认关闭。

参数必须满足 `run-timeout > timeout > stop-timeout > 0`；轮数、缓存数量和 TTL 必须为正。地址和参数校验失败时，不创建远端测试数据。

`run-timeout` 是协作式取消范围，并非独立进程强制终止计时器。当前 SDK 的同步调用没有接收这个 context，正在执行的调用仍受 SDK 自身连接、重试和 RPC 行为影响；清理还会使用独立超时。

### 2.4 与其他构建、测试入口的区别

```bash
make build
make build-x86
POLARIS_SERVER=127.0.0.1 make test
```

`make build` 只构建本机普通程序，`make build-x86` 只构建 Linux amd64 普通程序，都不会执行 E2E，也没有自动开启 race。`verify.sh` 会重新构建带 race 检测的本机程序。

`make test` 执行 `go test -race -count=1 -v ./...`。测试入口只从环境变量获取服务地址，全部地址变量未设置时明确输出 `SKIP`。因此，未配置服务端时看到测试进程退出成功，不能作为真实 E2E 通过的依据。

`POLARIS_NAMESPACE` 由 `e2e_test.go` 读取；直接执行 `verify.sh` 时应使用 `-namespace`。`make test SERVER=...` 不会把 Makefile 的 `SERVER` 自动转换成测试所需的 `POLARIS_SERVER`。

## 3. Shell 脚本如何启动验证

`verify.sh` 依次执行以下步骤：

1. 使用 `set -euo pipefail`，使构建错误、未定义变量和管道中的失败得到非零结果。
2. 根据脚本自身位置进入示例目录，保证从其他工作目录调用时，模块、产物和日志路径仍正确。
3. 创建 `.build/` 和 `.logs/`。
4. 读取 `GO`，未设置时使用 `go`，执行 `build -race -o .build/config-group-listener-cancel .`。
5. 构建成功后，生成包含日期、时间和脚本进程 ID 的 `.logs/verify-*.log` 路径。
6. 将所有参数通过 `"$@"` 原样传给 Go 程序，把 stdout 和 stderr 合并后交给 `tee`，同时显示和保存输出。
7. 通过 `pipefail` 保留验证程序的非零结果，避免程序失败但 `tee` 成功时被误判为成功。

构建失败发生在运行日志文件名生成之前，错误直接显示在终端。正常完成验证后，业务断言失败、清理失败或 race 检测报告都应使脚本返回非零状态；应同时核对最终日志和退出状态。

## 4. 初始化：创建独占数据和共享 SDK 分组

### 4.1 创建临时数据

`runE2E` 校验参数并建立整套测试的 context，然后创建 `fixture`：

1. 使用 8 字节随机数生成 16 位十六进制后缀。
2. 组名为 `e2e-listener-cancel-<随机后缀>`，文件名固定为 `listener.yaml`。
3. 在准备数据之前注册清理函数，后续即使准备失败也进入清理流程。
4. 通过 HTTP `POST /config/v1/configfilegroups` 创建配置组，成功后设置 `groupCreated`。
5. 通过 HTTP `POST /config/v1/configfiles` 创建未发布的 YAML 文件，成功后设置 `fileCreated`。

文件的初始内容为 `generation: initial`。后续更新和发布使用 Go SDK。

HTTP 请求会校验状态码为 2xx、响应 JSON 可解析、业务码等于 `Code_ExecuteSuccess`，不会仅凭 HTTP 200 判断成功。设置 token 时通过 `X-Polaris-Token` 请求头传递。

### 4.2 创建 SDK

程序创建一个 SDK context，在其中同时构造 `ConfigAPI` 和 `ConfigGroupAPI`，配置发现地址、配置地址及 token。配置连接的 connect timeout 和 message timeout 均设置为 3 秒。

整套测试复用这个 SDK context。缓存换代期间不销毁它，否则旧 fetcher 被回收可能只是因为整个 SDK 一起销毁，不能证明单个监听器已经摘除。

### 4.3 发布基线并验证两个公开入口

`initialize` 执行：

1. 调用 `publish` 发布第一份唯一内容，并取得服务端版本号。
2. 调用公开 `FetchConfigGroup`，请求设置 `Subscribe: true`、`Mode: SDKMode`。
3. 断言返回值支持 `model.CancelableConfigGroupListener`。
4. 再调用公开 `GetConfigGroup`，断言返回对象与前一次相同。
5. 通过旧 `AddChangeListener` 注册一个独立观察器 `observer`，整套测试期间保持它存活。
6. 等待 `group.GetFiles()` 包含本轮 `listener.yaml`，且版本等于刚确认的基线版本。

后续每个缓存都通过 `loadGroup` 再次调用 `FetchConfigGroup`，并断言拿到同一个 SDK 配置组对象。这保证测试持续向同一份监听器容器注册、取消，能够观察多代缓存带来的引用累积。

## 5. 每次发布如何识别对应事件

### 5.1 `publish`：内容确认与版本确认

每次调用 `publish` 都递增本地 `sequence`，生成以下形式的内容：

```yaml
run: e2e-listener-cancel-<本轮随机后缀>
generation: <本地发布序号>
```

随后依次执行：

1. `UpdateConfigFile` 更新本轮测试文件。
2. `PublishConfigFile` 发布该文件。
3. 通过独立的 `FetchConfigFile` 拉取文件，使用 `Subscribe: false`，避免建立该文件的长期订阅缓存。
4. 确认返回内容与本次生成的内容完全相同，且服务端 `Version` 大于上次确认的版本。
5. 若尚未满足条件，按 200ms 的 ticker 等待后继续尝试，直到满足条件或超时。
6. 保存服务端版本并输出 `[PUBLISH] generation=... version=...`。

`generation` 是程序的发布序号，`version` 是服务端文件版本，两者不能等同。代码只要求版本递增，没有假设版本从 1 开始或每次加 1。

这一步独立于分组事件。即使已经确认文件内容，后面仍必须等待配置组回调到达。

### 5.2 为什么按版本号，而不是按 MD5 匹配

在测试环境的实测中，分组元数据已经返回 `listener.yaml` 和有效版本，但 `Md5` 为空。若按本地内容 MD5 匹配，初始化会一直等待并超时，即使分组已经完成同步。

当前流程先独立确认文件内容，再使用它的服务端版本匹配分组事件中的 `SimpleConfigFile.Version`。这同时避免了“发布内容不正确”和“元数据省略 MD5”造成的误判。

### 5.3 `fetcherStats`：记录实际回调入口

每个 fetcher 拥有独立统计对象：

- `registrations`：监听器注册次数。
- `versions`：每个文件版本进入该回调的次数。
- `detached`：可取消模式下，SDK 取消函数执行完毕的信号。

`onConfigGroupChange` **先记录版本次数，再检查 `stopped` 标记**。因此，即使 `MarkStopping` 让业务处理直接返回，只要 SDK 仍调用旧回调，就会被统计出来。

## 6. `pulse` 的通过条件

多数场景使用 `pulse(ctx, active, retired)` 统一完成一次“发布—通知—回收”验证。

### 6.1 等待真实事件抵达

`pulse` 先调用 `publish` 获取本次版本，然后等待：

- 独立观察器对该版本的计数大于 0，证明共享 SDK 配置组仍能接收新事件。
- `active` 中每个缓存对该版本的计数大于 0，证明每个存活缓存都实际收到通知。

`waitUntil` 使用 20ms 的 ticker 检查条件。到达超时仍不满足时，报 `published event reaches active listeners` 错误。

### 6.2 检查旧对象是否回收

每个 `retiredFetcher` 只保存统计对象及 `weak.Pointer[configGroupFetcher]`。统计对象不反向引用 fetcher，弱引用也不延长 fetcher 生命周期。

调用方先等待取消闭包和后台协程退出，再释放缓存强引用。`pulse` 随后循环执行 `runtime.GC()`，直到所有待检查弱引用的 `Value()` 都为 `nil`，或等待超时。

这一步检查的是旧 fetcher 对象是否仍存活，不是比较进程 RSS 或堆大小。SDK 和配置组在检查期间继续存活，所以不会把销毁整个 SDK 带来的回收当成取消监听成功。

### 6.3 最终断言和日志字段

事件等待和 GC 等待共用 `pulse` 在发布确认后创建的同一个单步 context。最终检查：

- 每个存活缓存对本次版本的回调次数必须恰好为 1。
- 待检查旧缓存对本次版本的回调次数总和必须为 0。
- 待检查旧 fetcher 中仍存活的对象数量必须为 0。

检查日志示例：

```text
[CHECK] active=8 retired=160 retained=0 staleCallbacks=0
```

字段含义：

- `active`：本次检查的存活缓存数量，不包括独立观察器。
- `retired`：本次传入的旧缓存记录数量；不是全套场景累计值。
- `retained`：这些旧记录中，弱引用仍指向存活 fetcher 的数量。
- `staleCallbacks`：这些旧缓存针对**本次发布版本**进入回调的次数总和。

`retained` 或 `staleCallbacks` 非零时返回 `listener leak`。如果同时存在回收超时，错误会一起保留。

### 6.4 为什么需要同时检查事件和对象

只检查 `stopped` 标记无法证明 SDK 已摘除监听；只检查旧缓存没有业务输出，可能是停止标记挡住了业务处理；只检查旧缓存没有回调，可能是 SDK 根本没有收到新事件。

独立观察器、停止标记之前的回调计数、旧对象弱引用分别验证这三个环节，避免上述情况被误判为通过。

取消语义允许已经进入分发快照的回调继续执行，取消函数也不等待正在执行的回调。测试通过“等待取消完成后再发布一个新版本”检查后续事件；它不要求取消瞬间中断旧快照中的回调。

## 7. 缓存生命周期如何控制

### 7.1 启动和注册

`newResourceCache` 异步启动 `Run`。`Run` 创建独立的缓存生命周期 context，关闭 `started`，然后执行 WarmUp，即 `fetcher.Fetch(cacheCtx)`。

`Fetch` 首次取得共享配置组后注册方法值回调。在 `cancelable` 模式下，注册方式为：

```go
cancel := listener.AddChangeListenerWithCancel(f.onConfigGroupChange)
detached := f.stats.detached
context.AfterFunc(ctx, func() {
    cancel()
    close(detached)
})
```

这个取消闭包只捕获取消函数和完成信号，不直接捕获 `f`。监听器的生命周期取决于 `cacheCtx`；即使 context 在注册 `AfterFunc` 之前就已取消，仍会调度取消闭包。

### 7.2 正常停止和超时停止

`Stop` 使用 `sync.Once` 保证停止过程只执行一次，后续调用返回相同结果。它依次：

1. 等待 `started`，确保缓存 context 已建立。
2. 取消缓存 context，从而触发 SDK 监听器的异步取消。
3. 如果已经有后台工作，最多等待 `stop-timeout`。
4. 正常退出时调用 `Cleanup`，设置停止标记并清空 fetcher 的组引用，返回 `true`。
5. 等待超时时仅调用 `MarkStopping`，不清空组引用，返回 `false`。

WarmUp 尚未建立后台工作时，`workerDone` 为空，Stop 可以直接执行 Cleanup。晚注册场景利用这一点构造“先 Cleanup，后注册监听”。

### 7.3 阶段信号

- `started`：缓存 context 已创建；不代表 WarmUp 已完成。
- `ready`：WarmUp 已结束，可读取 `runErr`，且后台工作状态已经确定。
- `detached`：可取消模式下，SDK 取消函数已执行完。
- `runDone`：整个 `Run` 已返回。
- `registerGate`：阻塞监听注册，用于晚注册场景。
- `workerGate`：阻塞后台工作退出，用于 Stop 超时场景。

`awaitStopped` 必须同时等待 `detached` 和 `runDone`。只看到 `Stop` 返回，或 `cleaned/stopped` 标记变化，不能直接作为监听摘除完成的依据。

## 8. 五个场景的详细流程

五个场景按以下顺序串行执行。每个场景开始输出 `[RUN]`，成功后输出 `[PASS]`；任一场景失败时停止后续场景并进入清理。

### 8.1 请求 context 与正常 Stop

日志名：`request-context-and-normal-stop`。实现：`normalStop`。

1. 启动一个缓存，使用缓存生命周期 context 完成首次 WarmUp 和监听注册。
2. 另建一个模拟请求的 context，使用它对**已初始化的同一 fetcher**连续调用 5 次 `Fetch`。
3. 取消请求 context，断言注册次数仍为 1。
4. 发布一个新版本，断言存活缓存恰好收到一次事件，证明请求取消未摘掉缓存监听。
5. 连续调用两次 `Stop`，断言均走正常清理路径，且 `cleaned=true`。
6. 等待取消完成和 Run 退出，建立弱引用记录，并将缓存切片中的强引用置空。
7. 再发布一个新版本，断言独立观察器仍能收到事件，旧缓存回调数为 0，旧对象可回收。

典型检查结果为先 `active=1 retired=0`，后 `active=0 retired=1`，两次的 `retained` 和 `staleCallbacks` 均为 0。

该场景模拟请求 context 的取消，没有发起真实 HTTP 请求；首次注册由缓存 context 完成，后续请求只是读取已初始化的 fetcher。

### 8.2 Stop 超时，仅 MarkStopping

日志名：`stop-timeout-mark-stopping`。实现：`stopTimeout` → `stopBlockedCache(ctx, false)`。

1. 使用未开放的 `workerGate` 启动一个缓存，完成 WarmUp。
2. 先发布一个版本，确认该缓存原本可以正常接收回调。
3. 调用 `Stop`，缓存 context 被取消，但后台工作仍阻塞在 `workerGate`。
4. 等待超过 `stop-timeout` 后，断言 `Stop=false` 且 `cleaned=false`，确认只执行了 MarkStopping。
5. **在仍未开放 `workerGate` 时等待 `detached`**，证明取消监听不依赖后台工作退出或 Cleanup。
6. 开放 `workerGate`，等待 Run 真正退出，释放缓存强引用。
7. 发布后续版本，断言旧回调数和残留对象数均为 0。

测试刻意保留超时路径中的组引用。只有 SDK 摘除方法值回调后，旧 fetcher 才能在外部引用释放时被回收。

### 8.3 Cleanup 之后才注册监听

日志名：`late-registration-after-cleanup`。实现：`lateRegistration` → `stopBlockedCache(ctx, true)`。

1. 使用未开放的 `registerGate` 启动缓存，阻止 WarmUp 完成监听注册。
2. 等待 `started`，确认 context 已建立，然后调用 `Stop`。
3. 此时尚未创建后台工作的 `workerDone`，断言 Stop 正常返回且 `cleaned=true`。
4. Cleanup 完成后才开放 `registerGate`，允许 WarmUp 随后注册监听。
5. 新注册的监听器绑定到已经取消的缓存 context，因此 `context.AfterFunc` 会执行取消闭包。
6. 等待 `ready`、`detached` 和 `runDone`，释放缓存强引用。
7. 发布后续版本，断言旧回调数为 0，旧对象可回收。

该场景保证监听注册发生在 Cleanup 之后；`started` 本身只表示 context 已创建，不保证 SDK 拉取已经完成。

### 8.4 TTL 到期、多代缓存交叠

日志名：`ttl-expiration-overlapping-generations`。实现：`churn`。

1. 创建第一代 `fetchers` 个缓存，等待各自 WarmUp 完成，发布一次验证全部活监听器。
2. 每轮先创建下一代 `fetchers` 个缓存，此时新旧两代共享同一个 SDK 配置组。
3. 仅在第一轮的新旧交叠阶段额外发布一次，检查 `2 × fetchers` 个缓存各收到一次回调。
4. 为旧一代启动时长为 `ttl` 的 `time.AfterFunc`；定时器到期后逐个调用旧缓存的 Stop。
5. 等待定时器完成，再通过 `retireNormally` 重复调用 Stop、检查 Cleanup、等待取消与 Run 退出、释放强引用。
6. 将旧对象的统计和弱引用加入本场景累计记录，把新一代作为当前代，输出 `[TTL]`。
7. 完成所有轮次后发布一次，检查当前代正常接收，所有历史代均无残留回调、均可回收。
8. 停止最后一代，再发布一次，检查本场景全部缓存均无残留回调、均可回收。

默认 `rounds=20`、`fetchers=8` 时：

- 第一轮交叠检查有 16 个活缓存。
- 第 20 轮结束时有 8 个活缓存，以及 `20 × 8 = 160` 个旧缓存记录。
- 停止最后一代后，累计检查 `(20 + 1) × 8 = 168` 个旧缓存对象。

对应日志：

```text
[CHECK] active=16 retired=0 retained=0 staleCallbacks=0
[TTL] round=20/20 active=8 retired=160
[CHECK] active=8 retired=160 retained=0 staleCallbacks=0
[CHECK] active=0 retired=168 retained=0 staleCallbacks=0
```

168 只包含 TTL 场景创建的缓存，不包含前面三个场景的缓存、独立观察器或自取消回调。

本场景并非每轮都发布配置。它在初始代、首次交叠、全部换代完成、最后一代停止后共发布四次；增大 `rounds` 主要增加监听器注册和取消次数。TTL 定时器在下一代就绪后才建立，用于加速并控制生命周期时序，不代表实际 access 的完整淘汰调度。

### 8.5 回调内部取消自身

日志名：`callback-self-cancellation`。实现：`selfCancellation`。

1. 直接向共享配置组注册一个可取消回调，并保存返回的取消函数。
2. 回调先等待 `ready` 信号，确保取消函数已经赋值，避免注册返回前事件到达造成使用未初始化变量。
3. 发布第一个新版本；回调记录版本后，在 `sync.Once` 中调用自己的取消函数，并关闭 `canceled`。
4. 等待 `canceled`，断言该版本只被此回调记录一次，确认自取消可以完成。
5. 再发布第二个新版本，等待独立观察器收到事件。
6. 断言自取消回调对第二个版本的计数为 0。

该场景验证真实仓库分发期间可以自取消，且其他监听器继续工作。它不创建 `resourceCache`，因此没有对应的缓存回收计数。

## 9. 旧接口泄漏对照

运行：

```bash
bash verify.sh \
  -server 127.0.0.1 \
  -discovery-addr 127.0.0.1:8091 \
  -config-addr 127.0.0.1:8093 \
  -openapi http://127.0.0.1:8090 \
  -listener-mode legacy
```

`legacy` 模式中的 fetcher 使用旧 `AddChangeListener`，没有向缓存 context 注册取消闭包。其 `detached` 信号在注册后直接关闭，仅用于让对照实验继续走到回调和回收检查，并不表示发生了真实摘除。

预期在第一个场景的正常 Stop 之后：

1. Cleanup 虽然已经清空 fetcher 的组引用，但 SDK 配置组仍持有该 fetcher 的方法值回调。
2. 新版本发布后，旧方法值仍被调用；统计发生在停止标记判断之前，所以计入一次残留回调。
3. 方法值继续持有 fetcher，弱引用在 GC 后仍非空。
4. 等待回收超时后，输出 `retained=1 staleCallbacks=1`，并以 `listener leak` 失败。

```text
[CHECK] active=0 retired=1 retained=1 staleCallbacks=1
FAIL: request-context-and-normal-stop: listener leak: retired=1 retained=1 staleCallbacks=1
```

这是对照实验预期的非零结果，说明验证程序能够识别原来的泄漏。连接失败、鉴权失败、内容确认超时或清理失败都不能替代这一判据。

旧接口对照仍运行当前 SDK，只切换缓存的注册方式。初始化依然检查可取消接口，因此不是把旧版 SDK 二进制替换进来运行。

## 10. 失败传播、退出与清理

### 10.1 清理顺序

场景全部结束，或某个场景返回错误后，`runE2E` 的 defer 按注册逆序执行：先销毁 SDK context，再清理本轮远端数据。

清理函数使用独立的后台 context，超时为 HTTP 客户端超时的两倍；默认 HTTP 超时为 `20s`，清理 context 为 `40s`。即使主场景 context 已经超时，仍会尝试：

1. 若 `fileCreated=true`，通过 `DELETE /config/v1/configfiles` 删除本轮文件，query 参数为 `namespace`、`group`、`name`。
2. 若 `groupCreated=true`，通过 `DELETE /config/v1/configfilegroups` 删除本轮分组，query 参数为 `namespace`、`group`。
3. 使用 `errors.Join` 汇总清理错误，并与原场景错误合并。

即使第一个删除请求失败，代码仍尝试删除分组，但两步共用清理 context 的剩余时间。只处理本轮已确认创建成功的数据，不按前缀批量清理其他运行留下的组。

### 10.2 最终通过条件

成功运行应同时具备：

- 五个场景各输出一条 `[PASS]`。
- 本轮分组输出 `[CLEANUP] 已删除本轮分组 ...`。
- 最终输出 `PASS: 配置组监听取消 E2E 全部通过`。
- 脚本退出状态为 0，且没有 race 报告。

如果场景通过但清理失败，最终仍输出 FAIL。日志中的局部 `[PASS]` 或单条 `retained=0` 不能代表整套测试通过。

CLI 为 `SIGINT / SIGTERM` 建立了取消 context，正常返回路径会尝试清理。强制结束进程、机器退出，或创建请求在服务端成功但客户端未确认等情况，仍可能留下临时数据；此时应根据本轮 `[SETUP]` 记录检查具体组名。

### 10.3 测试结束后的本地清理

`cleanup.sh` 沿用其他 demo 的预览、确认和 `-f` 用法，依赖 Bash 和 `lsof`：

```bash
./cleanup.sh --dry-run
./cleanup.sh
./cleanup.sh -f --timeout 120
make clean
make clean CLEAN_ARGS=--dry-run
```

执行流程如下：

1. 根据脚本自身位置确定示例目录；列出 `.build/`、`.logs/`、`polaris/` 和直接构建的 `config_group_listener_cancel` 中实际存在的产物。
2. 通过 `lsof` 的可执行文件路径识别当前用户的本示例进程，支持相对路径启动；同名但位于其他目录的 demo 不会被选中。
3. `--dry-run` 只展示，既不发送信号也不删除文件。普通模式需要交互确认，`-f` 跳过确认；`make clean` 使用 `-f`，可通过 `CLEAN_ARGS` 传递额外参数。
4. 对确认选中的进程再次检查路径，再发送 SIGTERM，让 Go 程序执行已有的场景退出和远端清理逻辑。
5. 默认最多等待 60 秒，可通过 `--timeout` 设置为 1-3600 秒；如果进程仍未退出，脚本返回非零并保留产物，不自动发送 SIGKILL。
6. 再次确认没有匹配的示例进程后，删除上述本地产物。若发现确认期间启动的新进程，则保留产物并返回非零。

脚本停止的是该目录下构建出的示例二进制，不负责停止 `go run` 或 `go test` 生成的临时可执行文件，也不负责构建中的 Go 进程。调用清理前应先结束这些命令。

清理会删除前文的验证日志和 SDK 基础日志，应先完成日志检查或备份。本地清理完成只表示进程及本地产物已处理；远端删除仍由 E2E 负责，若其失败，应先根据 `[SETUP]` 组名处理，保留日志直到排查完成。

## 11. 日志位置和排查顺序

### 11.1 日志位置

- `.logs/verify-*.log`：验证程序的阶段、发布序号、检查结果和最终结果，由 `verify.sh` 保存。
- `polaris/log/base/polaris.log`：SDK 默认基础日志，可检查配置组拉取和事件内容。
- `polaris/log/network/polaris-network.log`：SDK 默认网络日志，可辅助检查连接问题。

`verify.sh` 保存的是进程 stdout/stderr，不会自动把 SDK 写入文件的全部日志拼进验证日志。使用 `-debug` 后，应同时查看 SDK 日志文件。

日志包含 `[SETUP]`、`[PUBLISH]`、`[RUN]`、`[CHECK]`、`[TTL]`、`[PASS]`、`[CLEANUP]` 等标记；其中 `[TTL]` 只出现在换代场景，具体出现顺序由场景流程决定。

### 11.2 关联监听器注册和取消

`AddChangeListenerWithCancel` 实际注册和摘除监听器时，都会向基础日志输出 INFO 记录，关键词分别是 `已注册配置分组监听器`、`已取消配置分组监听器`。默认 INFO 级别即可看到，无需开启 `-debug`。

```text
[Config][Group] 已注册配置分组监听器. namespace=default, group=test-group, listenerID=1, cancelableListenerCount=1, listenerCount=2
[Config][Group] 已取消配置分组监听器. namespace=default, group=test-group, listenerID=1, remainingCancelableListeners=0, remainingListeners=1
```

- `namespace`、`group`：监听器所属的配置组。
- `listenerID`：注册和取消关联使用的 ID，在同一 SDK 配置组对象内区分注册；不是全局 ID，也不是 DEBUG 分发日志中的数组下标。
- `cancelableListenerCount`、`listenerCount`：注册后可取消监听器数量和全部业务监听器数量。
- `remainingCancelableListeners`：摘除后剩余的可取消监听器数量。
- `remainingListeners`：摘除后剩余的全部业务监听器数量，包含旧接口监听器。示例保留了独立观察器，因此缓存全部停止后通常仍为 1。

每次有效注册打印一次；重复或并发调用同一个取消函数，只在真正摘除时打印一次。空回调不打印注册日志，其空操作取消函数也不会打印。数量在注册或删除操作持锁期间取得快照，日志在释放锁后写入；不同监听器的并发日志不保证按 ID 或数量排序。

取消日志表示该注册已被摘除，不表示已经进入分发快照或正在执行的回调被强制终止，也不表示整个配置组停止同步。后续新版本没有进入旧回调、旧对象可以回收，仍由 `[CHECK]` 断言确认。

重新构建并运行示例后，在示例目录筛选：

```bash
rg -n '已注册配置分组监听器|已取消配置分组监听器' polaris/log/base/polaris.log
```

历史运行日志不会自动补上这些记录。旧 `AddChangeListener` 没有取消入口，`legacy` 对照中的旧缓存不会产生这对生命周期日志。

### 11.3 重复日志如何精简

INFO 保留可取消监听器注册/取消、配置文件拉取成功摘要、配置文件实际变更和配置组变更摘要。文件变更的类型、加密标记、MD5、版本及监听器数量合并为一条记录，不输出配置内容。

以下过程细节使用 DEBUG：缓存未命中、文件仓库创建和首次拉取完成、过滤器执行、连接器/过滤链/仓库的分层耗时、本地缓存写入或删除成功、内部仓库事件、版本比较和未变更响应。删除本来就不存在的缓存文件也只记录 DEBUG；配置不存在、网络错误、实际磁盘错误等仍保留 WARN/ERROR。

配置组的 INFO 变更日志只保留组标识、revision、文件数量和业务监听器数量。完整 Before/After JSON 在 DEBUG 的 `配置分组变更详情` 中每次事件只打印一次，逐回调日志不再重复整份事件。仓库内部回调数量使用 `repoListenerCount`，避免与业务 `listenerCount` 混淆。

需要排查这些细节时使用 `-debug` 重新运行。各轮 `[TTL]` 和 `[CHECK]` 仍在 `.logs/verify-*.log`，SDK 基础日志记录注册、取消和配置变更。

### 11.4 根据失败位置定位

- **构建错误**：检查 Go 版本、`GOROOT`、依赖和 C 编译环境；此时尚未创建远端数据。
- **创建请求连接失败或业务码不成功**：检查管理 HTTP 地址、网络可达性、命名空间和 token 权限。
- **`confirm published config content and version` 超时**：SDK 尚未读到本次内容及递增版本，检查发布和文件拉取链路。
- **`initial published file visible` 超时**：文件确认已完成，但分组列表还未出现对应文件版本，检查配置组同步及 SDK 元数据日志。
- **`published event reaches active listeners` 超时**：独立观察器或至少一个活缓存没有记录本次版本，检查组同步、监听注册和回调执行。
- **`SDK listener cancellation` 或 `cancel during Stop timeout` 超时**：取消闭包未完成，检查缓存 context、监听注册时序和锁。
- **`active fetcher got ... callbacks`**：同一版本的回调次数不等于 1，检查重复注册或重复分发。
- **`retained > 0`**：旧 fetcher 仍有强引用；结合取消完成信号、后台工作退出情况和 SDK 方法值引用排查。
- **`staleCallbacks > 0`**：取消完成后的新版本仍进入旧回调，直接说明该检查对象存在残留监听。
- **`callback self cancellation` 超时**：自取消未完成，需结合事件是否到达判断是否存在分发持锁问题。
- **清理请求失败**：测试不能算整体通过，按 `[SETUP]` 的具体组名确认残留。
- **`WARNING: DATA RACE`**：查看报告中的访问栈；race 检查覆盖本次执行过的并发路径，不代表穷尽全部调度。

## 12. 已有实测记录与验证范围

### 12.1 2026 年 10 月 10 日的实测

实测服务地址已脱敏，文档统一以 `127.0.0.1` 示例表示。本次实测使用默认 `default` 命名空间、20 轮 TTL、每代 8 个缓存，以及带 race 检测的程序。

可取消模式运行于本机时间 17:08:27—17:08:53（Asia/Shanghai），五个场景全部通过。TTL 场景的最终记录为：

```text
[CHECK] active=0 retired=168 retained=0 staleCallbacks=0
[PASS] ttl-expiration-overlapping-generations
[PASS] callback-self-cancellation
PASS: 配置组监听取消 E2E 全部通过
```

旧接口对照运行于 17:18:01—17:18:26，在第一个场景检出 `retained=1 staleCallbacks=1` 并按预期失败。两次运行均记录了本轮临时分组清理成功。

本工作区保留的日志文件为 `.logs/verify-20261010-170826-91317.log` 和 `.logs/verify-20261010-171800-7024.log`。日志目录被 Git 忽略，其他机器拉取仓库后不一定存在这些文件。

### 12.2 与本地回归测试的配合

E2E 验证真实发布和缓存生命周期。空回调、相同回调多次独立注册、旧接口顺序、慢回调期间注册/取消、快照边界、并发注册/取消/分发等细节，由仓库中的本地回归测试进一步覆盖：

- [model_group_test.go](../../../pkg/flow/configuration/model_group_test.go)：监听器行为、并发和回收语义。
- [group_flow_test.go](../../../pkg/flow/configuration/group_flow_test.go)：两个 flow 入口及缓存命中时的可取消能力，取消后组同步继续进行。

在仓库根目录运行：

```bash
go test -race -count=1 ./pkg/flow/configuration ./pkg/model
```

本 E2E 使用单个随机配置组、单个文件和模拟缓存，不覆盖实际 access 业务实现、多个配置组的大规模压力、网络故障注入或所有可能的并发调度。修改监听器实现后，应结合上述本地测试与真实服务端 E2E 判断回归结果。
