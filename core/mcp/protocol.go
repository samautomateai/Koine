// Package mcp is a Model Context Protocol client written on the standard library: JSON-RPC 2.0 over stdio and streamable HTTP
// (including SSE responses), OAuth for remote servers, the mcp.json configuration, and the bridge that registers a server's tools and
// resources in the tool registry (DES-050). A server is third-party code: project-level configuration only runs once the folder is trusted.
package mcp

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the MCP revision this client asks for. A server may answer with an older one it supports.
const ProtocolVersion = "2025-06-18"

// Message is one JSON-RPC 2.0 message: a request (Method and ID), a notification (Method only) or a response (ID and Result or Error).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsResponse says whether m answers a request.
func (m *Message) IsResponse() bool { return m.Method == "" && len(m.ID) > 0 }

// RPCError is a JSON-RPC error object. It is also the error type a failed call returns.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// Tool is a tool as tools/list describes it.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  *Annotations    `json:"annotations,omitempty"`
}

// Annotations are the behaviour hints a server gives about a tool. They are hints, never a security boundary.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Content is one block of a tool result or resource: text, an image or audio (base64 Data), an embedded resource or a link.
type Content struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	Data     string    `json:"data,omitempty"`
	MimeType string    `json:"mimeType,omitempty"`
	Resource *Resource `json:"resource,omitempty"`
	URI      string    `json:"uri,omitempty"`
	Name     string    `json:"name,omitempty"`
}

// Resource is a resource as resources/list describes it, or the body resources/read returns for one (Text or Blob set).
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Text        string `json:"text,omitempty"`
	Blob        string `json:"blob,omitempty"`
}

// ResourceTemplate is an entry of resources/templates/list.
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// CallResult is a tools/call result.
type CallResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Capabilities are the parts of the server's capabilities this client looks at.
type Capabilities struct {
	Tools     *json.RawMessage `json:"tools,omitempty"`
	Resources *json.RawMessage `json:"resources,omitempty"`
}

// InitResult is the answer to initialize.
type InitResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Instructions string `json:"instructions,omitempty"`
}
