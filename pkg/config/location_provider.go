package config

import (
	"errors"
	"fmt"
)

type LocationProviderConfigImpl struct {
	Name    string `yaml:"name" json:"name"`
	Type    string `yaml:"type" json:"type"`
	Region  string `yaml:"region" json:"region"`
	Zone    string `yaml:"zone" json:"zone"`
	Campus  string `yaml:"campus" json:"campus"`
	Address string `yaml:"address" json:"address"`
}

func (l LocationProviderConfigImpl) Verify() error {
	if l.Type == "" {
		return errors.New("type is empty")
	}

	switch l.Type {
	case "local", "remoteHttp":
		if l.Region == "" || l.Zone == "" || l.Campus == "" {
			return errors.New(fmt.Sprintf("type is %s, region, zone, campus must be set", l.Type))
		}
	case "remoteService":
		if l.Address == "" {
			return errors.New(fmt.Sprintf("type is %s, address must be set", l.Type))
		}
	}
	return nil
}

func (l LocationProviderConfigImpl) SetDefault() {
}
