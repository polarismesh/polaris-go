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

package httpServer

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

const (
	PluginName = "httpServer"
)

// Server httpServer插件，实现Admin接口
type Server struct {
	*plugin.PluginBase
	pluginCtx  *plugin.InitContext
	host       string
	port       int
	httpServer *http.Server
	mux        *http.ServeMux
	once       sync.Once
	sErr       atomic.Value
}

// SetError 原子设置服务器错误
func (s *Server) SetError(err error) {
	s.sErr.Store(err)
}

// GetError 原子获取服务器错误
func (s *Server) GetError() error {
	if v := s.sErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func init() {
	plugin.RegisterConfigurablePlugin(&Server{}, &Config{})
}

// Type 插件类型
func (s *Server) Type() common.Type {
	return common.TypeAdmin
}

// Name 插件名称
func (s *Server) Name() string {
	return PluginName
}

// Init 插件初始化
func (s *Server) Init(ctx *plugin.InitContext) error {
	s.PluginBase = plugin.NewPluginBase(ctx)
	s.pluginCtx = ctx
	s.host = ctx.Config.GetGlobal().GetAdmin().GetHost()
	s.port = ctx.Config.GetGlobal().GetAdmin().GetPort()
	s.mux = http.NewServeMux()
	return nil
}

// Destroy 销毁插件，释放资源
func (s *Server) Destroy() error {
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(context.Background()); err != nil {
			log.GetBaseLogger().Errorf("[Admin][HttpServer] shutdown http server error: %v", err)
			return err
		}
		log.GetBaseLogger().Infof("[Admin][HttpServer] http server stopped")
	}
	if s.PluginBase != nil {
		return s.PluginBase.Destroy()
	}
	return nil
}

// RegisterHandler 注册HTTP处理器路径
func (s *Server) registerHandlers(adminHandlers []model.AdminHandler) {
	for _, adminHandler := range adminHandlers {
		path := adminHandler.Path
		handler := adminHandler.HandlerFunc
		s.mux.HandleFunc(path, handler)
		log.GetBaseLogger().Infof("[Admin][HttpServer] registered path: %s", path)
	}
}

// Run 启动HTTP服务器
func (s *Server) Run() {
	s.once.Do(func() {
		paths := s.pluginCtx.Config.GetGlobal().GetAdmin().GetPaths()
		if len(paths) == 0 {
			log.GetBaseLogger().Warnf("[Admin][HttpServer] no path registered, skip starting http server")
			return
		}
		s.registerHandlers(paths)
		addr := fmt.Sprintf("%s:%d", s.host, s.port)
		s.httpServer = &http.Server{
			Addr:    addr,
			Handler: s.mux,
		}
		go func() {
			log.GetBaseLogger().Infof("[Admin][HttpServer] starting http server on %s", addr)
			if err := s.httpServer.ListenAndServe(); err != nil {
				s.SetError(err)
				log.GetBaseLogger().Errorf("[Admin][HttpServer] http server error: %v", err)
			}
		}()
	})
}
