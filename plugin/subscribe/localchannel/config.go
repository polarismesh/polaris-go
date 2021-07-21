package localchannel

const (
	DefaultChannelBufferSize = 30
)

//上报缓存信息插件的配置
type Config struct {
	ChannelBufferSize uint32 `yaml:"channelBufferSize" json:"channelBufferSize"`
}

//校验配置
func (c *Config) Verify() error {
	return nil
}

//设置默认配置值
func (c *Config) SetDefault() {
	if c.ChannelBufferSize == 0 {
		c.ChannelBufferSize = DefaultChannelBufferSize
	}
}
