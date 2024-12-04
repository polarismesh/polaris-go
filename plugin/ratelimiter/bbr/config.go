package bbr

import (
	"fmt"
	"time"
)

type Config struct {
	CPUSampleInterval time.Duration // CPU使用率采样间隔
	Decay             int           // 加权平均系数
}

// SetDefault 设置默认值
func (c *Config) SetDefault() {
	if c.CPUSampleInterval <= 0 {
		c.CPUSampleInterval = time.Millisecond * 500
	}
}

// Verify 校验配置值
func (c *Config) Verify() error {
	if c.CPUSampleInterval <= 0 {
		return fmt.Errorf("Invalid CPUSampleInterval")
	}
	if c.Decay < 0 || c.Decay > 100 {
		return fmt.Errorf("Invalid Decay")
	}
	return nil
}
