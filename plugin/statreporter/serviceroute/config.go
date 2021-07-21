package serviceroute

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/plugin/statreporter/basereporter"
	"time"
)

//插件配置
type Config struct {
	ReportInterval *time.Duration `yaml:"reportInterval" json:"reportInterval"`
}

const (
	defaultReportInterval = 5 * time.Minute
)

//校验配置
func (c *Config) Verify() error {
	if c.ReportInterval == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"reportInterval of statReporter serviceRoute not configured")
	}
	if *c.ReportInterval < basereporter.MinReportInterval {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"invalid reportInterval %v for statReporter serviceRoute, which must be greater than or equal to %v",
			*c.ReportInterval, basereporter.MinReportInterval)
	}
	return nil
}

//设置默认值
func (c *Config) SetDefault() {
	if c.ReportInterval == nil {
		c.ReportInterval = model.ToDurationPtr(defaultReportInterval)
	}
}
