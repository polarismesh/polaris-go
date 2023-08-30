package cpubbr

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

// RateLimiterCpuBbr 基于CPU策略的限流控制器
type RateLimiterCpuBbr struct {
	*plugin.PluginBase
}

// Type 插件类型，这里是算 limiter 的一种
func (g *RateLimiterCpuBbr) Type() common.Type {
	return common.TypeRateLimiter
}

// Name 插件名，一个类型下插件名唯一
func (g *RateLimiterCpuBbr) Name() string {
	return config.DefaultCpuBbrRateLimiter
}

// Init 初始化插件
func (g *RateLimiterCpuBbr) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *RateLimiterCpuBbr) Destroy() error {
	return nil
}

// IsEnable 配置是否打开标记
func (g *RateLimiterCpuBbr) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// InitQuota 初始化并创建限流窗口
// 主流程会在首次调用，以及规则对象变更的时候，调用该方法
func (g *RateLimiterCpuBbr) InitQuota(criteria *ratelimiter.InitCriteria) ratelimiter.QuotaBucket {
	return createCpuBbrLimiter(criteria.DstRule.SystemResourceAmount)
}

// init 注册插件
func init() {
	plugin.RegisterPlugin(&RateLimiterCpuBbr{})
}
