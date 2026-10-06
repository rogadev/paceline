package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func server() *Server {
	return &Server{
		Name: "test", Version: "1.0", Instructions: "use the echo tool",
		Tools: []Tool{
			{
				Name: "echo", Description: "Echo the text.", InputSchema: json.RawMessage(`{"type":"object"}`),
				Call: func(args json.RawMessage) (string, error) {
					var a struct{ Text string }
					if err := json.Unmarshal(args, &a); err != nil {
						return "", err
					}
					if a.Text == "" {
						return "", errors.New("text is required")
					}
					return a.Text, nil
				},
			},
		},
	}
}

// exchange sends each line to a server and returns its responses by id.
func exchange(t *testing.T, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := server().Serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response is not one JSON object per line: %q", line)
		}
		responses = append(responses, m)
	}
	return responses
}

func TestInitializeNegotiatesVersion(t *testing.T) {
	tests := map[string]string{
		"2025-06-18": "2025-06-18",
		"2024-11-05": "2024-11-05",
		"2099-01-01": latestVersion,
	}
	for asked, want := range tests {
		got := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+asked+`"}}`)
		result := got[0]["result"].(map[string]any)
		if result["protocolVersion"] != want {
			t.Errorf("asked %s: got %v, want %s", asked, result["protocolVersion"], want)
		}
		if result["instructions"] != "use the echo tool" || result["serverInfo"].(map[string]any)["name"] != "test" {
			t.Errorf("initialize result missing server details: %v", result)
		}
		if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
			t.Error("initialize must advertise the tools capability")
		}
	}
}

func TestNotificationsGetNoResponse(t *testing.T) {
	got := exchange(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		``,
		`{"jsonrpc":"2.0","id":"a","method":"ping"}`,
	)
	if len(got) != 1 || got[0]["id"] != "a" {
		t.Errorf("want one response, to the ping with id \"a\": %v", got)
	}
}

func TestToolsListAndCall(t *testing.T) {
	got := exchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo"}}`,
	)
	tools := got[0]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" || tools[0].(map[string]any)["inputSchema"] == nil {
		t.Errorf("tools/list = %v", tools)
	}
	ok := got[1]["result"].(map[string]any)
	if ok["isError"] != nil || ok["content"].([]any)[0].(map[string]any)["text"] != "hi" {
		t.Errorf("successful call = %v", ok)
	}
	failed := got[2]["result"].(map[string]any)
	if failed["isError"] != true || failed["content"].([]any)[0].(map[string]any)["text"] != "text is required" {
		t.Errorf("a failing tool should return isError with the message: %v", failed)
	}
}

func TestProtocolErrors(t *testing.T) {
	tests := []struct {
		line string
		code float64
	}{
		{`{not json`, codeParse},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, codeInvalidRequest},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope"}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"x"}`, codeInvalidParams},
	}
	for _, tt := range tests {
		got := exchange(t, tt.line)
		if len(got) != 1 {
			t.Fatalf("%s: want one response, got %v", tt.line, got)
		}
		e, _ := got[0]["error"].(map[string]any)
		if e == nil || e["code"] != tt.code {
			t.Errorf("%s: error = %v, want code %v", tt.line, got[0]["error"], tt.code)
		}
		if _, ok := got[0]["id"]; !ok {
			t.Errorf("%s: an error response must carry an id, null if unknown", tt.line)
		}
	}
}

func TestServeStopsOnOversizedMessage(t *testing.T) {
	var out bytes.Buffer
	huge := `{"jsonrpc":"2.0","id":1,"method":"ping","params":"` + strings.Repeat("x", maxMessageBytes) + `"}`
	if err := server().Serve(strings.NewReader(huge), &out); err == nil {
		t.Error("Serve should stop with an error on a message over the cap")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestServeReportsWriteErrors(t *testing.T) {
	if err := server().Serve(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), failingWriter{}); err == nil {
		t.Error("Serve should report a failed write")
	}
}
