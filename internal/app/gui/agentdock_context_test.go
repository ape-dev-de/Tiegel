package gui

import (
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/agentchat"
)

// The context of a user message (D571) reaches the organism.
func TestChatEntryContext(t *testing.T) {
	gen := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	e := chatEntry(api.AgentEntry{Index: 3, Kind: api.EntryUser, Text: "q", Context: []api.AgentContextBlock{{
		Source: "knowledge", Title: "Context from project knowledge", Tokens: 321,
		Hits: []api.AgentContextHit{
			{Title: "Create invoice", Heading: "Data flow", Sources: []string{"src/A.php:4"}, GeneratedAt: gen, File: "/k/processes/invoice.md", Anchor: "data-flow"},
			{Title: "Clear cart", Outdated: true, Changed: []string{"src/C.php"}},
		}}}})
	if e.Kind != agentchat.User || len(e.Context) != 1 || e.Context[0].Tokens != 321 || len(e.Context[0].Hits) != 2 {
		t.Fatalf("%+v", e)
	}
	h := e.Context[0].Hits[0]
	if h.Title != "Create invoice" || h.Heading != "Data flow" || h.File != "/k/processes/invoice.md" || h.Anchor != "data-flow" || h.Generated == "" || h.Outdated {
		t.Fatalf("%+v", h)
	}
	if h2 := e.Context[0].Hits[1]; !h2.Outdated || h2.Generated != "" {
		t.Fatalf("%+v", h2)
	}
	if chatEntry(api.AgentEntry{Kind: api.EntryUser, Text: "q"}).Context != nil {
		t.Fatal("context without blocks")
	}
}
