package mcp

type Method string

const (
	InitializeMethod        Method = "initialize"
	InitializedNotification Method = "notification/initialized"

	ToolsListMethod              Method = "tools/list"
	ToolsCallMethod              Method = "tools/call"
	ToolsListChangedNotification Method = "tools/list_changed"
)

type InitializeRequest struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"clientCapabilities"`
	ClientInfo      ImplementationInfo `json:"clientInfo"`
}

type InitializeResponse struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ImplementationInfo `json:"serverInfo"`
	Instructions    string             `json:"instructions"`
}
