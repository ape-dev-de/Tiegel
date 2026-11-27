package knowledgetest

import (
	"context"
	"errors"
	"strings"
	"testing"

	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
)

func TestFakeAnswers(t *testing.T) {
	f := &Fake{Service: kn.Ref{File: "src/S.php", Symbol: `App\S::save`}}
	ctx := context.Background()
	prompts := map[string]string{
		kn.LevelOverview:    "## Task: overview (level 0)\nx",
		kn.LevelEntryPoints: "## Task: entry points (level 1) – HTTP routes\n\nInventory Tiegel extracted mechanically (…):\n- GET /a → App\\A::b (src/A.php:3)\n- public/index.php (public/index.php)\n\nKeep",
		kn.LevelProcesses:   "## Task: process (level 2) – A (HTTP)\n\nEntry points of this process:\n- GET /a → App\\A::b (src/A.php:3)\n\nStart with these files and follow the calls:\n- src/A.php\n\n- q-0011aabb: why?",
		kn.LevelComponents:  "## Task: component (level 3) – App\\S\n\nIts file:\n- src/S.php\n",
		kn.LevelQuestions:   "## Task: answer open questions\n\nQuestions:\n- q-0011aabb (asked by overview): a\n- q-0011aabc (asked by overview): b\n",
	}
	for level, p := range prompts {
		if l, step, _ := Level(p); l != level || step != kn.StepDescribe {
			t.Fatal(level, l, step)
		}
		d, err := kn.ParseDoc(level, f.Answer(ctx, p))
		if err != nil {
			t.Fatal(level, err)
		}
		switch d := d.(type) {
		case *kn.EntryPointsDoc:
			if len(d.Entries) != 2 || d.Entries[0].Handler.Line != 3 || d.Entries[1].Name != "public/index.php" {
				t.Fatalf("%+v", d.Entries)
			}
		case *kn.ProcessDoc:
			if len(d.Steps) != 2 || d.Steps[0].Symbol != `App\A::b` || d.Answered[0].ID != "q-0011aabb" {
				t.Fatalf("%+v", d)
			}
		case *kn.AnswersDoc:
			if len(d.Answered) != 1 || len(d.StillOpen) != 1 {
				t.Fatalf("%+v", d)
			}
		}
	}
	// The review returns the page unchanged with a confidence.
	v := f.Answer(ctx, "## Task: review (overview) – Overview\nThe page:\n```json\n{\"summary\":\"s\",\"containers\":[{\"name\":\"a\"}]}\n```\n")
	if val, d, err := kn.ParseValidation(kn.LevelOverview, v); err != nil || val.Confidence != 0.85 || d.(*kn.OverviewDoc).Summary != "s" {
		t.Fatal(v, err)
	}
	if f.Answer(ctx, "## Task: review (overview)\nno page") != "no page" || f.Answer(ctx, "hello") != "?" {
		t.Fatal("unknown prompts")
	}
	if l, _, _ := Level("nothing"); l != "" {
		t.Fatal(l)
	}
	f.Hook = func(context.Context, string, string) error { return errors.New("tired") }
	if !strings.Contains(f.Answer(ctx, prompts[kn.LevelOverview]), "tired") {
		t.Fatal("hook")
	}
	if f.Count("## Task: overview") != 2 || len(f.Prompts()) != 9 {
		t.Fatal(f.Count("## Task: overview"), len(f.Prompts()))
	}
	// The kn.Agent form; cancelled contexts fail.
	s, _ := f.Agent().NewSession(ctx)
	defer s.Close()
	if _, err := s.Prompt(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Prompt(cctx, "hello"); err == nil {
		t.Fatal("cancelled")
	}
	if f.Script() == nil || section("x", "y") != "" {
		t.Fatal("script")
	}
	comp := &Fake{}
	if d, err := kn.ParseDoc(kn.LevelComponents, comp.Answer(ctx, prompts[kn.LevelComponents])); err != nil || d.(*kn.ComponentDoc).Methods[0].Symbol != `App\S::run` {
		t.Fatal(err)
	}
}
