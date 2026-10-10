# 可取消的配置分组监听器

通过真实 Polaris 配置发布，验证 `model.CancelableConfigGroupListener` 的取消行为，以及缓存停止后回调所属对象能否被回收。示例模拟 access 的缓存生命周期；配置拉取、发布、分组缓存和事件分发使用本仓库 SDK。

## 接入方式

`ConfigFileGroup` 原有接口保持不变。`FetchConfigGroup` 和 `GetConfigGroup` 返回的对象可通过类型断言获得可取消监听能力：

```go
group, err := groupAPI.GetConfigGroup(namespace, groupName)
if err != nil {
    return err
}
listener, ok := group.(model.CancelableConfigGroupListener)
if !ok {
    return fmt.Errorf("config group does not support cancelable listeners")
}

cancel := listener.AddChangeListenerWithCancel(onGroupChange)
defer cancel()
```

- 每次注册独立生效，同一个回调注册两次会得到两个取消函数。
- 取消函数支持并发和重复调用，回调中可以取消自身。
- 传入 `nil` 时不注册，返回可安全调用的空操作函数。
- 取消后创建的分发快照不再包含该回调；已经进入快照的回调仍可能执行，取消函数不等待它结束。
- 旧 `AddChangeListener` 继续按注册顺序通知，可取消监听器之间不保证顺序。
- 取消监听不会停止 SDK 的配置组同步，也不会清除其他调用方的监听器。

缓存场景应把监听器绑定到**缓存生命周期 context**。使用支持 `context.AfterFunc` 的 Go 版本时，可以这样注册：

```go
cancel := listener.AddChangeListenerWithCancel(fetcher.onConfigGroupChange)
context.AfterFunc(cacheCtx, cancel)
```

缓存停止时取消 `cacheCtx`。即使停止等待超时、没有执行 `Cleanup`，或者 WarmUp 在 context 取消后才完成注册，监听器也会被异步摘除。`cacheCtx` 取消返回不代表摘除已经完成；需要严格确认时，应像本示例一样增加完成信号并等待。HTTP 请求的 context 不适合承担共享缓存的生命周期。

## 构建和本地检查

此示例是独立 Go module，使用 `replace github.com/polarismesh/polaris-go => ../../..` 引用本地 SDK。示例需要 Go 1.24 或更高版本，用 `weak.Pointer` 验证旧缓存对象被回收；SDK 本身没有引入这个依赖。race 检查还需要本机 C 编译器。

在本目录执行：

```bash
make build
make build-x86
make test
```

构建产物放在 `.build/`。`build-x86` 生成 Linux amd64 程序。`make test` 在未设置服务端地址时会明确跳过 E2E，此时只证明示例能够编译，不代表真实发布链路验证通过。

SDK 的本地回归测试不依赖服务端，在仓库根目录运行：

```bash
go test -race -count=1 ./pkg/flow/configuration ./pkg/model
```

## 真实服务端验证

准备一个可写的测试命名空间（默认 `default`）。账号需要创建、更新、发布和删除测试配置的权限。每次运行创建随机命名的 `e2e-listener-cancel-*` 分组和一个 `listener.yaml` 文件，并在退出时清理本轮数据。

```bash
export POLARIS_SERVER=127.0.0.1
# 启用鉴权的环境需提前设置 POLARIS_TOKEN。
make e2e

# 缩短压力场景并指定测试命名空间：
make e2e ARGS='-namespace test -rounds 5 -fetchers 4'
```

`POLARIS_SERVER` 只接受主机名或 IP，默认端口为发现 gRPC `8091`、配置 gRPC `8093`、管理 HTTP `8090`。也可分别指定三个地址：

```bash
export POLARIS_DISCOVERY_ADDR=127.0.0.1:8091
export POLARIS_CONFIG_ADDR=127.0.0.1:8093
export POLARIS_OPENAPI=http://127.0.0.1:8090
bash verify.sh -namespace test
```

token 仅从环境变量 `POLARIS_TOKEN` 读取。`verify.sh` 构建带 race 检测的程序，将输出保存在 `.logs/verify-*.log`，任何构建、连接、发布、断言或清理错误都会返回非零状态。

主要参数：

- `-rounds`：TTL 过期重建轮数，默认 20。
- `-fetchers`：每代共享同一 SDK 分组的缓存数量，默认 8。
- `-ttl`：模拟缓存 TTL，默认 `100ms`。
- `-stop-timeout`：等待后台工作退出的上限，默认 `100ms`。
- `-timeout`：单步检查超时，默认 `20s`。
- `-run-timeout`：整套 E2E 超时，默认 `5m`。
- `-debug`：启用 SDK debug 日志。

`-h` 可查看全部参数。使用 `go test` 执行真实 E2E 时，`POLARIS_NAMESPACE` 可指定命名空间。

## 验证场景与判据

1. 请求 context 取消不影响缓存监听；重复 Fetch 只注册一次；正常 Stop 后取消监听并回收对象。
2. Stop 等待超时只执行 `MarkStopping` 时，生命周期取消仍能摘除监听。
3. Cleanup 先完成，WarmUp 随后才注册监听时，已经取消的 context 仍能触发摘除。
4. TTL 多轮换代期间新旧缓存短暂共存；历史缓存不再收到新发布的回调，且可以被回收。
5. 在真实分组回调内取消自身，不发生死锁，后续事件仍能到达其他监听器。

每次发布使用不同内容，通过独立的 SDK 文件拉取确认内容并取得服务端发布版本号，再按版本匹配分组事件。分组元数据可能不包含 MD5，因此不能依赖它识别发布。计数发生在 `MarkStopping` 判断之前，避免停止标记掩盖残留监听。独立的旧接口观察器证明同一 SDK 分组仍持续同步；弱引用证明旧 fetcher 没有被残留方法值保留。

成功时每个场景输出 `[PASS]`，回收检查输出 `retained=0 staleCallbacks=0`，最终输出 `PASS: 配置组监听取消 E2E 全部通过`。

SDK 基础日志 `polaris/log/base/polaris.log` 在 INFO 级别记录 `已注册配置分组监听器` 和 `已取消配置分组监听器`，可按同一组内的 `listenerID` 关联。缓存、过滤链、落盘及分层耗时等过程日志使用 DEBUG，完整配置组事件也只在 DEBUG 输出一次。运行阶段、回调和回收断言见 `.logs/verify-*.log`；详细说明见 [test.md](./test.md)。

可以使用旧接口做泄漏对照，预期以非零状态退出并报告 `listener leak`：

```bash
make e2e ARGS='-listener-mode legacy'
```

若连接或清理先失败，不能将该失败当作成功复现泄漏。清理失败时日志保留本轮随机分组名；排查后可针对该分组处理残留数据。

## 清理本地产物

参照其他 demo，提供 `cleanup.sh`，需要 Bash 和 `lsof`：

```bash
./cleanup.sh --dry-run           # 预览进程和待删除路径
./cleanup.sh                     # 展示并确认后清理
./cleanup.sh -f                  # 跳过确认
make clean                      # 等同于 bash cleanup.sh -f
make clean CLEAN_ARGS=--dry-run  # 通过 Makefile 预览
```

脚本只匹配当前用户运行的本示例二进制，先发送 SIGTERM，默认等待 60 秒，让 E2E 执行远端临时配置清理。可用 `--timeout 120` 延长等待；超时则返回失败并保留本地产物，不自动发送 SIGKILL。

进程退出后删除本目录的 `.build/`、`.logs/`、`polaris/` 和直接 `go build` 生成的 `config_group_listener_cancel`。其中 `polaris/` 包含 SDK 日志和缓存，清理前请保存需要保留的验证日志。脚本依据自身位置定位，不依赖调用时的工作目录。

该脚本负责本地清理，不另行查询或删除远端配置组。若前一次测试报告远端清理失败，应先依据 `.logs/` 的具体组名处理残留，再清理本地日志。
