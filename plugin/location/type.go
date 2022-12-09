package location

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
)

type LocationPlugin interface {
	Init(ctx *plugin.InitContext) error
	GetLocation() (*model.Location, error)
	Name() string
	GetPriority() int
}

type LocationPlugins []LocationPlugin

func (p LocationPlugins) Len() int {
	return len(p)
}

func (p LocationPlugins) Less(i, j int) bool {
	return p[i].GetPriority() < p[j].GetPriority()
}

func (p LocationPlugins) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}
