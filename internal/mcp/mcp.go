// Package mcp is a minimal Model Context Protocol server over stdio: enough
// of the protocol to offer tools to a client such as Claude Code.
//
// The stdio transport is newline-delimited JSON-RPC 2.0. A tools-only server
// needs four methods: initialize, ping, tools/list, and tools/call. Writing
// them on the standard library keeps paceline free of dependencies.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

// maxMessageBytes bounds one incoming message. Tool arguments here are a few
// KB at most.
const maxMessageBytes = 1 << 20

// latestVersion is offered when the client asks for a version this server
// does not know. Every version listed has the same tools methods.
const latestVersion = "2025-06-18"

var knownVersions = []string{"2024-11-05", "2025-03-26", latestVersion, "2025-11-25"}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Tool is one tool the server offers. Call returns the text shown to the
// model, or an error that is reported to the model as a failed tool call.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Call        func(args json.RawMessage) (string, error)
}

// Server answers MCP requests with a fixed set of tools.
type Server struct {
	Name, Version string
	Instructions  string
	Tools         []Tool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Serve handles messages from r until it ends, writing responses to w. It
// returns nil at end of input, or the read or write error that stopped it.
// Calls run one at a time, so tools never race each other.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	in := bufio.NewScanner(r)
	in.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	out := bufio.NewWriter(w)
	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		if resp := s.handle(line); resp != nil {
			data, err := json.Marshal(resp)
			if err != nil {
				return err
			}
			if _, err := out.Write(append(data, '\n')); err != nil {
				return err
			}
			if err := out.Flush(); err != nil {
				return err
			}
		}
	}
	return in.Err()
}

func failure(id json.RawMessage, code int, msg string) *response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// handle answers one message, or returns nil for a notification.
func (s *Server) handle(line []byte) *response {
	if line[0] == '[' {
		return failure(nil, codeInvalidRequest, "batch requests are not supported")
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return failure(nil, codeParse, "parse error: "+err.Error())
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return failure(req.ID, codeInvalidRequest, "not a JSON-RPC 2.0 request")
	}
	// A message without an id is a notification, such as
	// notifications/initialized, and never gets a response.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil
	}
	ok := func(result any) *response { return &response{JSONRPC: "2.0", ID: req.ID, Result: result} }

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := latestVersion
		if slices.Contains(knownVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		result := map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": s.Name, "version": s.Version},
		}
		if s.Instructions != "" {
			result["instructions"] = s.Instructions
		}
		return ok(result)
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
		}
		return ok(map[string]any{"tools": tools})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return failure(req.ID, codeInvalidParams, "invalid params: "+err.Error())
		}
		i := slices.IndexFunc(s.Tools, func(t Tool) bool { return t.Name == p.Name })
		if i < 0 {
			return failure(req.ID, codeInvalidParams, "unknown tool: "+p.Name)
		}
		if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
			p.Arguments = json.RawMessage("{}")
		}
		text, err := s.Tools[i].Call(p.Arguments)
		if err != nil {
			return ok(callResult{Content: []content{{Type: "text", Text: err.Error()}}, IsError: true})
		}
		return ok(callResult{Content: []content{{Type: "text", Text: text}}})
	}
	return failure(req.ID, codeMethodNotFound, "method not found: "+req.Method)
}
