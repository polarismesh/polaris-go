/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */
//
//@Author: springliao
//@Description:
//@Time: 2021/11/11 17:09

package utils

import (
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	Region          string = "region"
	Zone            string = "zone"
	TencentCloudApi string = "http://metadata.tencentyun.com/latest/meta-data/placement/%s"
)

var (
	regionVal string = ""
	zoneVal   string = ""
)

func GetInstanceLocation() *model.Location {
	return getLocationInTencentCloud()
}

func getLocationInTencentCloud() *model.Location {

	log.GetBaseLogger().Infof("start to get location metadata in cloud env")

	if regionVal == "" {
		if region, err := sendRequest(Region); err == nil {
			regionVal = region
		} else {
			log.GetBaseLogger().Infof("get region info fail : %s, but not affect", err.Error())
		}
	}
	if zoneVal == "" {
		if zone, err := sendRequest(Zone); err == nil {
			zoneVal = zone
		} else {
			log.GetBaseLogger().Infof("get zone info fail : %s, but not affect", err.Error())
		}
	}

	location := &model.Location{
		Region: "",
		Zone:   GetRegion(),
		Campus: GetZone(),
	}

	log.GetBaseLogger().Infof("get location info from cloud env : %s", location.String())

	return location
}

func sendRequest(typ string) (string, error) {
	reqApi := fmt.Sprintf(TencentCloudApi, typ)
	resp, err := http.Get(reqApi)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	val, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("curl %s failed", reqApi)
	}

	return string(val), nil
}

func GetRegion() string {
	return regionVal
}

func GetZone() string {
	return zoneVal
}
