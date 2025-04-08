package config

import (
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

type EventReporterConfigImpl struct {
	// 是否启动上报
	Enable *bool `yaml:"enable" json:"enable"`
	// 上报插件链
	Chain []string `yaml:"chain" json:"chain"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

func (e *EventReporterConfigImpl) IsEnable() bool {
	return *e.Enable
}

func (e *EventReporterConfigImpl) SetEnable(enable bool) {
	e.Enable = &enable
}

func (e *EventReporterConfigImpl) GetChain() []string {
	return e.Chain
}

func (e *EventReporterConfigImpl) SetChain(chain []string) {
	e.Chain = chain
}

func (e *EventReporterConfigImpl) GetPluginConfig(name string) BaseConfig {
	value, ok := e.Plugin[name]
	if !ok {
		return nil
	}
	return value.(BaseConfig)
}

func (e *EventReporterConfigImpl) Verify() error {
	return e.Plugin.Verify()
}

func (e *EventReporterConfigImpl) SetDefault() {
	if nil == e.Enable {
		enable := DefaultEventReporterEnabled
		e.Enable = &enable
		return
	}

	e.Plugin.SetDefault(common.TypeEventReporter)
}

func (e *EventReporterConfigImpl) SetPluginConfig(plugName string, value BaseConfig) error {
	return e.Plugin.SetPluginConfig(common.TypeEventReporter, plugName, value)
}

// Init 配置初始化.
func (e *EventReporterConfigImpl) Init() {
	e.Plugin = PluginConfigs{}
	e.Plugin.Init(common.TypeEventReporter)
}
