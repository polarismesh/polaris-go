package event

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

type EventReporter interface {
	plugin.Plugin

	ReportEvent(e model.BaseEvent) error
}

func init() {
	plugin.RegisterPluginInterface(common.TypeEventReporter, new(EventReporter))
}
