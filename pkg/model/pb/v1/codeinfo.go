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

package v1

const (
	ExecuteSuccess              uint32 = 200000
	DataNoChange                       = 200001
	ParseException                     = 400001
	EmptyRequest                       = 400002
	BatchSizeOverLimit                 = 400003
	InvalidDiscoverResource            = 400004
	InvalidRequestID                   = 400100
	InvalidUserName                    = 400101
	InvalidUserToken                   = 400102
	InvalidParameter                   = 400103
	InvalidNamespaceName               = 400110
	InvalidNamespaceOwners             = 400111
	InvalidNamespaceToken              = 400112
	InvalidServiceName                 = 400120
	InvalidServiceOwners               = 400121
	InvalidServiceToken                = 400122
	InvalidInstanceID                  = 400130
	InvalidInstanceHost                = 400131
	InvalidInstancePort                = 400132
	InvalidServiceAlias                = 400133
	InvalidNamespaceWithAlias          = 400134
	HealthCheckNotOpen                 = 400140
	HeartbeatOnDisabledIns             = 400141
	HeartbeatExceedLimit               = 400142
	HeartbeatException                 = 400143
	InvalidMetadata                    = 400150
	NotFoundMeshConfig                 = 400177
	ExistedResource                    = 400201
	NotFoundResource                   = 400202
	NamespaceExistedServices           = 400203
	ServiceExistedInstances            = 400204
	ServiceExistedRoutings             = 400205
	NotFoundService                    = 400301
	NotFoundRouting                    = 400302
	NotFoundInstance                   = 400303
	NotFoundServiceAlias               = 400304
	NotFoundNamespace                  = 400305
	NotFoundSourceService              = 400306
	ClientAPINotOpen                   = 400401
	NotAllowAliasUpdate                = 400501
	NotAllowAliasCreateInstance        = 400502
	NotAllowAliasCreateRouting         = 400503
	NotAllowCreateAliasForAlias        = 400504
	Unauthorized                       = 401000
	IPRateLimit                        = 403001
	APIRateLimit                       = 403002
	CMDBNotFindHost                    = 404001
	ExecuteException                   = 500000
	StoreLayerException                = 500001
	CMDBPluginException                = 500002
	ParseRoutingException              = 500004
)
