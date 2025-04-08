package event

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

type Proxy struct {
	EventReporter
	engine model.Engine
}

func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine model.Engine) {
	p.EventReporter = plug.(EventReporter)
	p.engine = engine
}

func init() {
	plugin.RegisterPluginProxy(common.TypeEventReporter, &Proxy{})
}
