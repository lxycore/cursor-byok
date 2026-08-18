// module.go 负责把 forwarder service 装配成 legacy HTTP/Connect handler。
package forwarder

import (
	"net/http"

	"connectrpc.com/connect"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/workspace"
)

type Module struct {
	Service                  *Service
	LocalBidiHandler         http.Handler
	LocalRunSSE              http.Handler
	AiHandler                http.Handler
	RepositoryServiceHandler http.Handler
	UploadServiceHandler     http.Handler
}

// NewModule 创建 forwarder 模块，并导出本地 Bidi / RunSSE 处理器。
func NewModule(historyRoot string, channelService modeladapter.ChannelResolver) *Module {
	return NewModuleWithWorkspace(historyRoot, channelService, nil)
}

// NewModuleWithWorkspace 创建 forwarder 模块，允许注入外部 workspace 管理器。
// workspace 管理器是进程级数据层，必须跨 rebuild 复用（否则前端持有的
// 管理器实例会与记录端分离，导致"刷新也看不到新记录"）。
func NewModuleWithWorkspace(historyRoot string, channelService modeladapter.ChannelResolver, ws *workspace.Manager) *Module {
	service := NewServiceWithWorkspace(historyRoot, channelService, ws)
	legacyBidiAppendProcedure := "/aiserver.v1.BidiService/BidiAppend"
	legacyRunSSEProcedure := "/agent.v1.AgentService/RunSSE"
	return &Module{
		Service:                  service,
		LocalBidiHandler:         connect.NewUnaryHandler(legacyBidiAppendProcedure, service.BidiAppend),
		LocalRunSSE:              NewLegacyRunSSEHandler(legacyRunSSEProcedure, service.RunSSE),
		AiHandler:                newAIHandler(service),
		RepositoryServiceHandler: newRepositoryServiceHandler(service),
		UploadServiceHandler:     newUploadServiceHandler(service),
	}
}
