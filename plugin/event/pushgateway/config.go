package pushgateway

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/plugin"
)

func init() {
	plugin.RegisterConfigurablePlugin(&PushgatewayReporter{}, &Config{})
}

type Config struct {
	Address        string `yaml:"address"`
	ReportPath     string `yaml:"reportPath"`
	EventQueueSize int    `yaml:"eventQueueSize"` // 队列满了，立即上报；定期每秒上报一次

}

// Verify verify config
func (c *Config) Verify() error {
	if c.Address == "" {
		return fmt.Errorf("global.eventReporter.plugin.pushgateway.address is empty")
	}

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
}
