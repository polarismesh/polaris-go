package pushgateway

import (
	"github.com/polarismesh/polaris-go/pkg/plugin"
)

func init() {
	plugin.RegisterConfigurablePlugin(&PushgatewayReporter{}, &Config{})
}

const (
	DefaultNamespaceName = "Polaris"
	DefaultServiceName   = "polaris.pushgateway"
)

type Config struct {
	Address        string `yaml:"address"`
	NamespaceName  string `yaml:"namespaceName"`
	ServiceName    string `yaml:"serviceName"`
	ReportPath     string `yaml:"reportPath"`
	EventQueueSize int    `yaml:"eventQueueSize"` // 队列满了，立即上报；定期每秒上报一次

}

// Verify verify config
func (c *Config) Verify() error {
	return nil
}

// SetDefault Setting defaults
func (c *Config) SetDefault() {
	if c.EventQueueSize == 0 {
		c.EventQueueSize = 512
	}
	if c.ReportPath == "" {
		c.ReportPath = "polaris/client/events" // 默认地址
	}
	if c.ServiceName == "" || c.NamespaceName == "" {
		c.ServiceName = DefaultServiceName
		c.NamespaceName = DefaultNamespaceName
	}
}
