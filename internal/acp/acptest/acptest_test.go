package acptest

import (
	"context"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
)

type nopHandler struct{ texts []string }

func (h *nopHandler) SessionUpdate(n acp.SessionNotification) {
	h.texts = append(h.texts, n.Update.Chunk().Text)
}
func (*nopHandler) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.Cancelled(), nil
}
func (*nopHandler) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, nil
}
func (*nopHandler) WriteTextFile(context.Context, acp.WriteTextFileRequest) error { return nil }

func TestEchoAgent(t *testing.T) {
	a := &Agent{Models: []string{"m/one"}}
	h := &nopHandler{}
	p, err := a.Start(context.Background(), acp.Command{}, h)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: 1}); err != nil {
		t.Fatal(err)
	}
	s, err := p.NewSession(ctx, "/")
	if err != nil || a.Model() != "m/one" {
		t.Fatal(err, a.Model())
	}
	res, err := p.Prompt(ctx, s.SessionID, []acp.ContentBlock{
		{Type: "resource", Resource: &acp.EmbeddedResource{URI: "file:///x", Text: "ctx "}}, acp.TextBlock("hi")})
	if err != nil || res.StopReason != acp.StopEndTurn {
		t.Fatal(err, res)
	}
	if a.Prompts()[0] != "ctx hi" || len(h.texts) != 2 || h.texts[1] != "ctx hi" {
		t.Fatal(a.Prompts(), h.texts)
	}
	if err := p.Conn().Call(ctx, "nope", nil, nil); acp.ErrorCode(err) != acp.CodeMethodNotFound {
		t.Fatal(err)
	}
	if *Str("x") != "x" {
		t.Fatal("Str")
	}
}
