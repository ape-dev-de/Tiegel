package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// client is a fake MCP client over pipes.
type client struct {
	t   *testing.T
	w   io.WriteCloser
	r   *bufio.Scanner
	err chan error
}

func start(t *testing.T, s *Server) *client {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	c := &client{t: t, w: cw, r: bufio.NewScanner(cr), err: make(chan error, 1)}
	go func() {
		c.err <- s.Serve(context.Background(), sr, sw)
		_ = sw.Close()
	}()
	t.Cleanup(func() { _ = cw.Close() })
	return c
}

func (c *client) send(raw string) {
	c.t.Helper()
	if _, err := io.WriteString(c.w, raw+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) recv() map[string]any {
	c.t.Helper()
	if !c.r.Scan() {
		c.t.Fatal("no response", c.r.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(c.r.Bytes(), &m); err != nil {
		c.t.Fatal(err, c.r.Text())
	}
	return m
}

func TestProtocol(t *testing.T) {
	s := &Server{Name: "test", Version: "1.0", Instructions: "use it", Tools: []Tool{
		{Name: "echo", Description: "echoes", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			Call: func(_ context.Context, args json.RawMessage) (string, error) {
				var a struct{ Text string }
				_ = json.Unmarshal(args, &a)
				return "echo: " + a.Text, nil
			}},
		{Name: "fail", Call: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("boom") }},
		{Name: "panic", Call: func(context.Context, json.RawMessage) (string, error) { panic("oops") }},
		{Name: "empty"},
	}}
	c := start(t, s)
	c.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	r := c.recv()
	res := r["result"].(map[string]any)
	if res["protocolVersion"] != "2025-03-26" || res["serverInfo"].(map[string]any)["name"] != "test" || res["instructions"] != "use it" || res["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatal(r)
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	c.send(`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if r := c.recv(); r["id"] != "p" || r["result"] == nil {
		t.Fatal(r)
	}
	c.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := c.recv()["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 4 || tools[0].(map[string]any)["name"] != "echo" || tools[1].(map[string]any)["inputSchema"].(map[string]any)["type"] != "object" {
		t.Fatal(tools)
	}
	c.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	r = c.recv()
	content := r["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || content["text"] != "echo: hi" || r["result"].(map[string]any)["isError"] != nil {
		t.Fatal(r)
	}
	for _, name := range []string{"fail", "panic", "empty"} {
		c.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"` + name + `"}}`)
		r = c.recv()
		if r["result"].(map[string]any)["isError"] != true {
			t.Fatal(name, r)
		}
	}
	c.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope"}}`)
	if e := c.recv()["error"].(map[string]any); e["code"].(float64) != CodeInvalidParams {
		t.Fatal(e)
	}
	c.send(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":"bad"}`)
	if e := c.recv()["error"].(map[string]any); e["code"].(float64) != CodeInvalidParams {
		t.Fatal(e)
	}
	c.send(`{"jsonrpc":"2.0","id":7,"method":"resources/list"}`)
	if e := c.recv()["error"].(map[string]any); e["code"].(float64) != CodeMethodNotFound {
		t.Fatal(e)
	}
	c.send(`{not json`)
	if e := c.recv()["error"].(map[string]any); e["code"].(float64) != CodeParse {
		t.Fatal(e)
	}
	// Unknown protocol versions get the latest; empty lines are skipped.
	c.send("")
	c.send(`{"jsonrpc":"2.0","id":8,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if v := c.recv()["result"].(map[string]any)["protocolVersion"]; v != LatestProtocol {
		t.Fatal(v)
	}
	_ = c.w.Close()
	select {
	case err := <-c.err:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not end on EOF")
	}
}

func TestServeCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	defer w.Close()
	done := make(chan error, 1)
	go func() { done <- (&Server{}).Serve(ctx, r, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not cancelled")
	}
	// A broken writer ends the server.
	sr := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	if err := (&Server{}).Serve(context.Background(), sr, failWriter{}); err == nil {
		t.Fatal("write error ignored")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }
