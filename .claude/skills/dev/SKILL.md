# polaris-go 开发技能手册

> 从 commit `5af3794` 之后的提交记录中总结的代码需求编写方式与开发规范。

---

## 一、新增功能的完整开发流程

### 1. 新增插件类型（以 lossless 无损上下线为例）

一个完整的插件功能开发需要修改以下层次的文件，**严格按照分层架构**：

#### 第一层：模型层（pkg/model/）
- 定义数据模型和常量
- 文件命名：`pkg/model/<feature>.go`（如 `lossless.go`）
- 包含：常量定义、结构体、辅助方法（如 `IsXxxEnabled()`、`GetJsonString()`）
- 如有事件模型，在 `pkg/model/event/` 下新建对应文件（如 `lossless.go`、`instance.go`）

```go
// 常量使用全大写下划线命名
const (
    LosslessDelayRegisterStrategyDelayByTime = "DELAY_BY_TIME"
)

// 支持的策略用 map[string]struct{} 做校验集合
var SupportedDelayRegisterStrategies = map[string]struct{}{
    LosslessDelayRegisterStrategyDelayByTime: {},
}

// 模型结构体提供 IsXxxEnabled() 判断方法
type LosslessInfo struct { ... }
func (l *LosslessInfo) IsDelayRegisterEnabled() bool { ... }
```

#### 第二层：配置层（pkg/config/）
- 文件命名：`pkg/config/<feature>.go`（如 `lossless.go`、`admin.go`、`weight_adjust.go`）
- 必须实现接口并用 `var _ XxxConfig = (*XxxConfigImpl)(nil)` 编译期校验
- 每个配置实现必须包含：`Verify()`、`SetDefault()`、`Init()` 方法
- Getter/Setter 成对出现
- 插件配置通过 `PluginConfigs` 管理
- 默认值常量命名：`DefaultXxxYyy`（如 `DefaultLosslessEnabled`）

```go
var _ LosslessConfig = (*LosslessConfigImpl)(nil)

type LosslessConfigImpl struct {
    Enable *bool         `yaml:"enable" json:"enable"`
    Type   string        `yaml:"type" json:"type"`
    Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

func (l *LosslessConfigImpl) Verify() error { ... }
func (l *LosslessConfigImpl) SetDefault() { ... }
func (l *LosslessConfigImpl) Init() { ... }
```

#### 第三层：插件接口层（pkg/plugin/<feature>/）
- 接口定义文件：`pkg/plugin/<feature>/<feature>.go`
- 代理文件：`pkg/plugin/<feature>/proxy.go`
- 接口通过 `init()` 注册：`plugin.RegisterPluginInterface(common.TypeXxx, new(Xxx))`
- Proxy 通过 `init()` 注册：`plugin.RegisterPluginProxy(common.TypeXxx, &Proxy{})`

```go
// 接口定义
type Lossless interface {
    plugin.Plugin
    PreProcess(*model.InstanceRegisterRequest, *model.ServiceRuleResponse)
    Process() (*model.InstanceRegisterResponse, error)
    PostProcess()
}

func init() {
    plugin.RegisterPluginInterface(common.TypeLossless, new(Lossless))
}
```

```go
// Proxy 定义
type Proxy struct {
    Lossless
    engine model.Engine
}

func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine model.Engine) {
    p.Lossless = plug.(Lossless)
    p.engine = engine
}

func init() {
    plugin.RegisterPluginProxy(common.TypeLossless, &Proxy{})
}
```

#### 第四层：插件实现层（plugin/<feature>/<pluginName>/）
- 实现文件拆分为多个文件：`base.go`（初始化）、`config.go`（配置）、`process.go`（核心逻辑）等
- 通过 `init()` 注册：`plugin.RegisterConfigurablePlugin(&XxxController{}, &Config{})`
- 必须实现 `Type()`、`Name()`、`Init()` 方法
- 使用 `*plugin.PluginBase` 嵌入基础能力

```go
func init() {
    plugin.RegisterConfigurablePlugin(&LosslessController{}, &Config{})
}

type LosslessController struct {
    *plugin.PluginBase
    pluginCtx *plugin.InitContext
    pluginCfg *Config
    log       log.Logger
}

func (p *LosslessController) Type() common.Type { return common.TypeLossless }
func (p *LosslessController) Name() string { return PluginName }
func (p *LosslessController) Init(ctx *plugin.InitContext) error { ... }
```

#### 第五层：流程编排层（pkg/flow/）
- 新功能的流程编排放在独立文件：`pkg/flow/<feature>_flow.go`（如 `lossless_flow.go`）
- 在 `pkg/flow/impl.go` 的 `Engine` 结构体中添加插件字段
- 在 `InitFlowEngine()` 中初始化插件

```go
// Engine 中添加字段
type Engine struct {
    lossless lossless.Lossless
    weightAdjuster []weightadjuster.WeightAdjuster
}

// InitFlowEngine 中初始化
flowEngine.lossless, err = data.GetLossless(cfg, plugins)
```

#### 第六层：SDK 引擎接口层（pkg/sdk/engine.go）
- 在 `Engine` 接口中添加新方法声明
- 如需避免循环导入，在此定义辅助接口

#### 第七层：API 层
- 内部 API：`api/provider.go`（接口定义）+ `api/provider_impl.go`（实现）
- 外部 API：根目录 `api.go`（接口定义）+ `api_provider.go`（实现）
- 外部 API 是对内部 API 的类型转换包装

```go
// 内部 API
func (c *providerAPI) LosslessRegister(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
    if err := checkAvailable(c); err != nil { return nil, err }
    if err := instance.Validate(); err != nil { return nil, err }
    instance.AutoHeartbeat = true
    return c.context.GetEngine().SyncLosslessRegister(&instance.InstanceRegisterRequest)
}

// 外部 API（类型转换包装）
func (p *providerAPI) LosslessRegister(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
    instance.AutoHeartbeat = true
    return p.rawAPI.LosslessRegister((*api.InstanceRegisterRequest)(instance))
}
```

#### 第八层：插件注册
- 在 `pkg/plugin/register/plugins.go` 中添加 `_` import
- 接口包和实现包都需要注册

```go
import (
    _ "github.com/polarismesh/polaris-go/pkg/plugin/lossless"      // 接口
    _ "github.com/polarismesh/polaris-go/plugin/lossless/losslessController"  // 实现
)
```

#### 第九层：日志扩展
- 在 `pkg/log/default.go` 中添加日志级别常量、日志名、日志路径
- 在 `pkg/log/log.go` 中添加 Logger 枚举值、Set/Get 方法
- 在 `pkg/log/ctx_logger.go` 中添加 ContextLogger 字段和方法
- 在 `api/log.go` 中添加日志级别设置逻辑

#### 第十层：配置文件
- 在 `polaris.yaml` 中添加新功能的默认配置段

---

## 二、事件模型编写模式

### 事件分层设计
```
pkg/model/event/
├── base.go       # BaseEvent 接口 + BaseEventImpl 结构体 + EventName 常量
├── instance.go   # InstanceEvent 接口 + 实现 + 工厂函数
├── config.go     # ConfigEvent 接口 + 实现 + 工厂函数
└── lossless.go   # LosslessEvent 接口 + 实现 + 工厂函数（嵌入 InstanceEventImpl）
```

### 事件工厂函数模式
```go
func GetLosslessEvent(eventName EventName, losslessInfo model.LosslessInfo) BaseEventImpl {
    return BaseEventImpl{
        BaseType:  LosslessEventType,
        EventType: LosslessEventType.EventTypeString(),
        EventName: eventName,
        EventTime: time.Now().Format("2006-01-02 15:04:05"),
        LosslessEvent: &LosslessEventImpl{
            InstanceEventImpl: InstanceEventImpl{
                Namespace: losslessInfo.Instance.Namespace,
                Service:   losslessInfo.Instance.Service,
            },
            LosslessInfo: losslessInfo,
        },
    }
}
```

---

## 三、单元测试编写模式

### 测试文件组织
- 测试文件放在被测包内：`plugin/loadbalancer/weightedrandom/wr_test.go`
- 使用中文测试函数名提高可读性：`TestChooseInstance_基本选择`、`TestChooseInstance_静态权重_等权重分布`
- 测试文件同样需要 `//` 许可证头（`Copyright (C) 2021 Tencent. All rights reserved.`，`*.pb.go` 除外）

### 测试辅助工具模式
```go
// ==================== 测试辅助工具 ====================

// newClusterTestInstance 创建带 localValue 的测试实例
func newClusterTestInstance(id string, weight uint32, port uint32) model.Instance { ... }

// buildTestServiceInstances 构建测试用的 ServiceInstances 和 Cluster
func buildTestServiceInstances(instances []model.Instance) (model.ServiceInstances, *model.Cluster) { ... }
```

### 测试分类命名
```go
// ==================== ChooseInstance 静态权重路径测试 ====================
func TestChooseInstance_基本选择(t *testing.T) { ... }
func TestChooseInstance_静态权重路径_返回实例属于原始列表(t *testing.T) { ... }

// ==================== 静态权重分布测试 ====================
func TestChooseInstance_静态权重_等权重分布(t *testing.T) { ... }
func TestChooseInstance_静态权重_不同权重影响分布(t *testing.T) { ... }
```

### 概率分布测试模式
```go
totalRounds := 100000
tolerance := 0.03
expectedRatio := 1.0 / 3.0
for _, inst := range instances {
    actualRatio := float64(counts[inst.GetId()]) / float64(totalRounds)
    if math.Abs(actualRatio-expectedRatio) > tolerance {
        t.Errorf("实例 %s 等权重下选中比例 %.4f 偏离期望 %.4f 超过容忍度 %.2f",
            inst.GetId(), actualRatio, expectedRatio, tolerance)
    }
}
```

---

## 四、Examples 编写模式

### 目录结构
```
examples/<feature>/
├── consumer/
│   ├── Makefile
│   ├── go.mod          # 独立 module
│   ├── go.sum
│   ├── main.go
│   ├── polaris.yaml
│   └── Dockerfile      # 可选
├── provider/<variant>/
│   ├── Makefile
│   ├── go.mod
│   ├── main.go
│   └── polaris.yaml
├── cleanup.sh          # 清理脚本
├── verify_<scenario>.sh  # 验证脚本
└── README.md / README-zh.md
```

### Example main.go 模式
```go
var (
    selfNamespace   string
    selfService     string
    port            int
    configPath      string
)

func initArgs() {
    flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
    flag.StringVar(&selfService, "selfService", "ServiceName", "selfService")
    flag.IntVar(&port, "port", 18080, "port")
    flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
}

type PolarisClient struct {
    provider polaris.ProviderAPI
    consumer polaris.ConsumerAPI
    host     string
}
```

---

## 五、CI/CD 与 Lint 配置

### golangci-lint v2 配置要点
- 配置文件：`.golangci.yml`，版本声明 `version: "2"`
- `new-from-rev` 不能放在配置文件中（v2 不支持），必须通过命令行参数 `--new-from-rev=origin/main` 传递
- v2 中已移除的属性：`ignore-words`（改为 `extra-words`）、`ignore-generated-header`、`ignore-tests`、`skip-generated`
- CI 使用 `golangci/golangci-lint-action@v9`，版本 `v2.11.4`

### GitHub Actions 版本
- `actions/checkout@v4`（需 `fetch-depth: 0` 以获取完整历史）
- `actions/setup-go@v5`
- Go 版本矩阵：`[1.21.x, 1.22.x, 1.23.x, 1.24.x]`

### 本地 lint 命令
```bash
git fetch origin main
golangci-lint run --timeout=30m --new-from-rev=origin/main
```

---

## 六、代码规范要点

### 许可证头（`**/*.go` 必须包含，`*.pb.go` 除外）

使用 `//` 行注释。版权行固定为 `Copyright (C) 2021 Tencent. All rights reserved.`。CI 由 `license-checker.yml` 按 `.licenserc.yaml` 检查整段头。

```go
// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
```

### Import 分组顺序
```go
import (
    // 标准库
    "fmt"
    "time"

    // 第三方库
    apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

    // 项目内部包
    "github.com/polarismesh/polaris-go/pkg/model"
    "github.com/polarismesh/polaris-go/pkg/plugin"
)
```

### 日志规范
- 日志前缀使用方括号标识模块：`[LosslessController]`、`[Lossless Event]`
- 使用模块专属 Logger（如 `p.log`），而非全局 Logger
- 错误日志使用 `Errorf`，信息日志使用 `Infof`

### 错误处理
- 配置校验错误使用 `fmt.Errorf("provider.lossless.xxx must not be empty")`
- 插件初始化错误直接返回 `error`
- 事件上报错误使用 `continue` 跳过，不中断主流程

### 并发安全
- 使用 `sync.RWMutex` 保护共享数据
- 使用 `sync.Once` 确保初始化只执行一次（懒加载模式）
- 使用 `*bool` 区分"未设置"和"设置为 false"

---

## 七、提交信息规范

### 提交前缀
- `feat:` — 新功能（如 `feat: lossless`、`feat: warmup`）
- `fix:` — 修复（如 `fix: 无损上下线规则使用服务端新字段获取`）
- `chore:` — 杂项（如 `chore: update examples`）
- 无前缀 — 中文描述的功能/优化提交（如 `优化event reporter的退出顺序问题`）

### 提交粒度
- **大功能**一次提交（如 lossless 70 个文件一次提交）
- **优化/修复**按功能点拆分小提交
- **CI/Lint 修复**独立提交
- **Examples 更新**可以独立提交

---

## 八、配置文件编写模式（polaris.yaml）

### 新增配置段模式
```yaml
provider:
  lossless:
    enable: true
    type: losslessController
    strategy: DELAY_BY_TIME
    delayRegisterInterval: 30s
    healthCheckInterval: 3s
    plugin:
      losslessController: {}
  weightAdjust:
    enable: true
    chain:
      - warmup
    plugin:
      warmup: {}
  admin:
    host: 0.0.0.0
    port: 28080
    type: httpServer
    plugin:
      httpServer: {}
```

---

## 九、常见开发任务清单

### 新增一个插件类型
1. `pkg/plugin/common/plugin.go` — 添加 `TypeXxx` 常量
2. `pkg/model/` — 定义数据模型
3. `pkg/config/` — 定义配置结构和接口
4. `pkg/config/api.go` — 在配置接口中添加 Getter
5. `pkg/config/impl.go` — 在配置实现中添加字段和初始化
6. `pkg/config/default.go` — 添加默认值常量
7. `pkg/plugin/<feature>/` — 定义插件接口和 Proxy
8. `plugin/<feature>/<name>/` — 实现插件
9. `pkg/flow/` — 编排流程
10. `pkg/sdk/engine.go` — 扩展引擎接口
11. `api/` — 扩展 API 接口和实现
12. 根目录 `api.go` + `api_<role>.go` — 扩展外部 API
13. `pkg/plugin/register/plugins.go` — 注册 import
14. `pkg/log/` — 扩展日志（如需要）
15. `polaris.yaml` — 添加默认配置
16. `examples/` — 添加使用示例
17. 编写单元测试

### 新增一个负载均衡算法
1. `plugin/loadbalancer/<name>/` — 实现 `loadbalancer.LoadBalancer` 接口
2. `plugin.cfg` — 注册插件
3. `pkg/plugin/register/plugins.go` — 添加 import
4. `plugin/loadbalancer/<name>/<name>_test.go` — 编写单元测试
5. `examples/loadbalancer/<name>/` — 添加示例

### 修复 lint 问题
1. 读取 `.golangci.yml` 确认规则
2. 按 lint 报错逐文件修复
3. 本地验证：`golangci-lint run --timeout=30m --new-from-rev=origin/main`
4. 独立提交 lint 修复