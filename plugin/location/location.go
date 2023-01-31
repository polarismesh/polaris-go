package location

import (
	"sort"

	"github.com/pkg/errors"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/plugin/location/local"
	"github.com/polarismesh/polaris-go/plugin/location/remotehttp"
	"github.com/polarismesh/polaris-go/plugin/location/remoteservice"
)

const (
	PriorityLocal = iota
	PriorityRemoteHttp
	PriorityRemoteService
)

type ProviderType = string

const (
	Local         ProviderType = "local"
	RemoteHttp    ProviderType = "remoteHttp"
	RemoteService ProviderType = "remoteService"
)

// 定义类型的优先级
var priority = map[ProviderType]int{
	Local:         PriorityLocal,
	RemoteHttp:    PriorityRemoteHttp,
	RemoteService: PriorityRemoteService,
}

// GetPriority 获取Provider的优先级
func GetPriority(typ ProviderType) int {
	return priority[typ]
}

const (
	ProviderName string = "chain"
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&Provider{})
}

// Provider 从环境变量获取地域信息
type Provider struct {
	*plugin.PluginBase
	pluginChains LocationPlugins
}

// Init 初始化插件
func (p *Provider) Init(ctx *plugin.InitContext) error {
	log.GetBaseLogger().Infof("start use env location provider")
	p.PluginBase = plugin.NewPluginBase(ctx)

	providers := ctx.Config.GetGlobal().GetLocation().GetProviders()
	p.pluginChains = make([]LocationPlugin, 0, len(providers))
	for _, provider := range providers {
		switch provider.Type {
		case Local:
			plugin, err := local.New(ctx)
			if err != nil {
				log.GetBaseLogger().Errorf("create local location plugin error: %v", err)
				return err
			}
			p.pluginChains = append(p.pluginChains, plugin)
		case RemoteHttp:
			plugin, err := remotehttp.New(ctx)
			if err != nil {
				log.GetBaseLogger().Errorf("create remoteHttp location plugin error: %v", err)
				return err
			}
			p.pluginChains = append(p.pluginChains, plugin)
		case RemoteService:
			plugin, err := remoteservice.New(ctx)
			if err != nil {
				log.GetBaseLogger().Errorf("create remoteService location plugin error: %v", err)
				return err
			}
			p.pluginChains = append(p.pluginChains, plugin)
		default:
			log.GetBaseLogger().Errorf("unknown location provider type: %s", provider.Type)
			return errors.New("unknown location provider type")
		}
	}
	// 根据优先级对插件进行排序
	sort.Sort(p.pluginChains)
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (p *Provider) Destroy() error {
	return p.PluginBase.Destroy()
}

// Type 插件类型
func (p *Provider) Type() common.Type {
	return common.TypeLocationProvider
}

// Name 插件名称
func (p *Provider) Name() string {
	return ProviderName
}

// GetLocation 获取地理位置信息
func (p *Provider) GetLocation() (*model.Location, error) {
	location := &model.Location{}

	for _, plugin := range p.pluginChains {
		tmp, err := plugin.GetLocation()
		if err != nil {
			log.GetBaseLogger().Errorf("get location from plugin %s error: %v", plugin.Name(), err)
			continue
		}
		location = tmp
	}
	return location, nil
}
