package bbr

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	"github.com/polarismesh/polaris-go/plugin/ratelimiter/bbr/core"
	"github.com/polarismesh/polaris-go/plugin/ratelimiter/bbr/cpu"
)

// BBRPlugin 基于 CPU BBR 策略的限流控制器
type BBRPlugin struct {
	*plugin.PluginBase
}

// Type 插件类型，这里是算 limiter 的一种
func (g *BBRPlugin) Type() common.Type {
	return common.TypeRateLimiter
}

// Name 插件名，一个类型下插件名唯一
func (g *BBRPlugin) Name() string {
	return config.DefaultBBRRateLimiter
}

// Init 初始化插件
func (g *BBRPlugin) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	if err := cpu.Init(); err != nil {
		return err
	}
	go core.CollectCPUStat()
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *BBRPlugin) Destroy() error {
	return nil
}

// IsEnable 配置是否打开标记
func (g *BBRPlugin) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// InitQuota 初始化并创建限流窗口
// 主流程会在首次调用，以及规则对象变更的时候，调用该方法
func (g *BBRPlugin) InitQuota(criteria *ratelimiter.InitCriteria) ratelimiter.QuotaBucket {
	return createBBRPlugin(criteria.DstRule)
}

// init 注册插件
func init() {
	plugin.RegisterPlugin(&BBRPlugin{})
}
