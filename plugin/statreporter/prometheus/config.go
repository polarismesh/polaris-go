// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissionsr and limitations under the License.
//

package prometheus

import (
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/plugin/statreporter"
)

const (
	defaultReportInterval = 1 * time.Minute
)

// Config
type Config struct {
	// JobName
	JobName string `yaml:"jobName"`
	// InstanceName
	InstanceName string `yaml:"instanceName"`
	// PushInterval
	PushInterval *time.Duration `yaml:"pushInterval"`
}

// Verify verify config
func (c *Config) Verify() error {
	if c.JobName == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"jobName of statReporter rateLimitRecord not configured")
	}
	if c.InstanceName == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"instanceName of statReporter rateLimitRecord not configured")
	}
	if *c.PushInterval < statreporter.MinReportInterval {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"invalid pushInterval %v for statReporter rateLimitRecord, which must be greater than or equal to %v",
			*c.PushInterval, statreporter.MinReportInterval)
	}
	return nil
}

// SetDefault Setting defaults
func (c *Config) SetDefault() {
	if nil == c.PushInterval {
		c.PushInterval = model.ToDurationPtr(defaultReportInterval)
	}
}
