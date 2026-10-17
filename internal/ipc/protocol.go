package ipc

import "encoding/json"

// JSON-RPC 2.0 style messages over named pipe / native messaging.

type Request struct {
	JSONRPC         string          `json:"jsonrpc"`
	ID              interface{}     `json:"id,omitempty"`
	ClientRequestID string          `json:"clientRequestId,omitempty"`
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type EventMessage struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"` // e.g. "event.task.progress"
	Params  interface{} `json:"params"`
}

const JSONRPCVersion = "2.0"

func OK(id interface{}, result interface{}) Response {
	return Response{JSONRPC: JSONRPCVersion, ID: id, Result: result}
}

func Fail(id interface{}, code int, msg string, data interface{}) Response {
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg, Data: data},
	}
}

// Method names
const (
	MethodPing              = "app.ping"
	MethodStatus            = "app.status"
	MethodDownloadAdd       = "download.add"
	MethodDownloadPause     = "download.pause"
	MethodDownloadResume    = "download.resume"
	MethodDownloadRemove    = "download.remove"
	MethodDownloadList      = "download.list"
	MethodMediaProbe        = "media.probe"
	MethodMediaStart        = "media.start"
	MethodTorrentProbe      = "torrent.probe"
	MethodTorrentStart      = "torrent.start"
	MethodGrabProbe         = "grab.probe"
	MethodGrabEnqueue       = "grab.enqueue"
	MethodBatchAdd          = "batch.add"
	MethodSettingsGet       = "settings.get"
	MethodSettingsSet       = "settings.set"
	MethodIntegrationRepair = "integration.repair"
	MethodIntegrationStatus = "integration.status"
	MethodFSChooseDir       = "fs.chooseDir"
	MethodSubscribe         = "events.subscribe"
)

const (
	ErrParse         = -32700
	ErrInvalidReq    = -32600
	ErrNoMethod      = -32601
	ErrInvalidParams = -32602
	ErrInternal      = -32603
	ErrAppStarting   = -32001
	ErrAppOffline    = -32002
	ErrForbidden     = -32003
)
