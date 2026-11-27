package orchestratetest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/orchestrate"
)

type nopHandler struct{ text strings.Builder }

func (h *nopHandler) SessionUpdate(n acp.SessionNotification) {
	if c := n.Update.Chunk(); n.Update.Kind == acp.UpdateAgentMessage {
		h.text.WriteString(c.Text)
	}
}
func (h *nopHandler) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.Cancelled(), nil
}
func (h *nopHandler) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, nil
}
func (h *nopHandler) WriteTextFile(context.Context, acp.WriteTextFileRequest) error { return nil }

func TestFakeAndRunner(t *testing.T) {
	f := New(map[string]Reply{"final": Static("plain")})
	h := &nopHandler{}
	p, err := f.Agent.Start(context.Background(), acp.Command{}, h)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx := context.Background()
	if _, err := p.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	s, _ := p.NewSession(ctx, t.TempDir())
	for _, text := range []string{"Role: critic\n## Diff of the tests", "Role: summarizer. Write the final report", "unknown"} {
		if _, err := p.Prompt(ctx, s.SessionID, []acp.ContentBlock{acp.TextBlock(text)}); err != nil {
			t.Fatal(err)
		}
	}
	if f.Calls("critic-tests") != 1 || f.Calls("final") != 1 || !strings.Contains(h.text.String(), `"issues":[]`) || !strings.Contains(h.text.String(), "plain") {
		t.Fatal(h.text.String())
	}
	dir := t.TempDir()
	r := &Runner{}
	if rep, _ := r.Run(ctx, orchestrate.TestRequest{Dir: dir}); rep.Error == "" {
		t.Fatal("tests without files")
	}
	_ = os.MkdirAll(filepath.Join(dir, "tests"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "tests", "CalcTest.php"), []byte(GoodTest), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "src", "Calc.php"), []byte(BaseCalc), 0o644)
	if rep, _ := r.Run(ctx, orchestrate.TestRequest{Dir: dir, Coverage: true}); rep.Green() || rep.CoverageNote == "" {
		t.Fatal(rep)
	}
	_ = os.WriteFile(filepath.Join(dir, "src", "Calc.php"), []byte(GoodCalc), 0o644)
	if rep, _ := r.Run(ctx, orchestrate.TestRequest{Dir: dir}); !rep.Green() || r.Count() != 3 {
		t.Fatal(rep)
	}
}
