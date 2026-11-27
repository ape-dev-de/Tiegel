package knowledge

import (
	"fmt"
	"slices"
	"strings"
)

// Levels of the top-down analysis (D565). A run works through them in
// this order; every level reuses what the earlier ones found.
const (
	// LevelOverview: project structure, tech stack, architecture style
	// and tooling (mostly mechanical) with a C4-style context/container
	// diagram.
	LevelOverview = "overview"
	// LevelEntryPoints: routes, console commands, queue consumers, event
	// listeners, scheduled tasks and public PHP files.
	LevelEntryPoints = "entrypoints"
	// LevelProcesses: one data flow per process (a group of related
	// entry points) traced through the code.
	LevelProcesses = "processes"
	// LevelComponents: the classes the processes touch most with their
	// data contracts.
	LevelComponents = "components"
	// LevelQuestions: answer the open questions of the earlier levels.
	LevelQuestions = "questions"
	// LevelAll runs every level.
	LevelAll = "all"
)

// Levels are the levels in run order.
var Levels = []string{LevelOverview, LevelEntryPoints, LevelProcesses, LevelComponents, LevelQuestions}

// LevelTitle is the human title of a level.
func LevelTitle(level string) string {
	switch level {
	case LevelOverview:
		return "Overview"
	case LevelEntryPoints:
		return "Entry points"
	case LevelProcesses:
		return "Processes"
	case LevelComponents:
		return "Components"
	case LevelQuestions:
		return "Open questions"
	}
	return level
}

// LevelRank is the position of a level in the run order (len(Levels)
// for unknown levels).
func LevelRank(level string) int {
	if i := slices.Index(Levels, level); i >= 0 {
		return i
	}
	return len(Levels)
}

// ParseLevels turns a level parameter ("", "all", one level or a comma
// separated list) into the levels to run, in run order.
func ParseLevels(s string) ([]string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == LevelAll {
		return slices.Clone(Levels), nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "entry-points", "entrypoint", "routes":
			p = LevelEntryPoints
		case "process":
			p = LevelProcesses
		case "component":
			p = LevelComponents
		case "question", "answer", "answers":
			p = LevelQuestions
		}
		if !slices.Contains(Levels, p) {
			return nil, fmt.Errorf("unknown level %q (overview, entrypoints, processes, components, questions or all)", p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return LevelRank(a) - LevelRank(b) })
	return out, nil
}

// Item is one piece of work of the pipeline: a page of one level.
type Item struct {
	// ID is stable and file-name safe ("overview", "entrypoints-http",
	// "process-app-controller-ordercontroller").
	ID    string `json:"id"`
	Level string `json:"level"`
	Title string `json:"title"`
	// MD is the page relative to the knowledge directory ("" for a batch
	// of questions: answers go to questions.md).
	MD string `json:"md,omitempty"`
	// Group is the entry point kind (entry points), the handler class or
	// file (processes) or the class (components).
	Group string `json:"group,omitempty"`
	// Files are the project relative sources the page depends on.
	Files []string `json:"files"`
	// Bytes is the size of the files (filled by ItemHashes).
	Bytes int64 `json:"bytes"`
	// Entries are the entry points of the page (entry points, processes).
	Entries []EntryPoint `json:"entries,omitempty"`
	// Questions are the question IDs of a question batch.
	Questions []string `json:"questions,omitempty"`
	// Inputs are further staleness inputs ("@facts" → hash).
	Inputs map[string]string `json:"inputs,omitempty"`
	// UsedBy are the processes touching a component (titles).
	UsedBy []string `json:"usedBy,omitempty"`
}

// Data is the validated JSON of the page relative to the knowledge
// directory (reused as context by later levels).
func (it Item) Data() string { return "data/" + it.ID + ".json" }
