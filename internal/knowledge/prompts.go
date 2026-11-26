package knowledge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxListedFiles bounds the file list in a prompt.
const maxListedFiles = 200

const readOnlyRules = `Rules:
- Only read. Do not create, change or delete files, do not run shell commands, do not use the web. Such tool calls are rejected.
- Use your read, grep and glob tools on the files below; follow calls into other files of the project when a flow leaves the unit.
- Paths are relative to the project root.`

func fileList(u Unit) string {
	var b strings.Builder
	for i, f := range u.Files {
		if i == maxListedFiles {
			fmt.Fprintf(&b, "- … and %d more files below %s\n", len(u.Files)-maxListedFiles, u.Dir)
			break
		}
		b.WriteString("- " + f + "\n")
	}
	return b.String()
}

// DescribePrompt asks the first agent for the data flows of u.
func DescribePrompt(project string, u Unit) string {
	return fmt.Sprintf(`You document the data flows of one part of the PHP project %q for a knowledge base that other developers and coding agents will search.

Unit: %s (directory %s, %d files)
%s
%s

Describe the data flows of this unit:
- entry points: HTTP controllers and routes, CLI commands, jobs, event listeners, public service methods used from outside,
- inputs and how they are validated and transformed,
- persistence: database tables, ORM entities, repositories, files, cache,
- external calls: HTTP APIs, queues, mail, other services,
- events dispatched and handled.

Keep it compact and factual: at most 12 flows, at most 10 steps per flow. Every step names a file you have read and the symbol (Class::method or function) that does it.

Answer with exactly one fenced json block in this shape (no other JSON):
%s
`, project, u.Title, u.Dir, len(u.Files), fileList(u), readOnlyRules, "```json\n"+DescriptionSchema+"\n```")
}

// ValidatePrompt asks the second agent to check a description against
// the code. notes are the findings of Tiegel's mechanical check.
func ValidatePrompt(project string, u Unit, d Description, notes []string) string {
	b, _ := json.MarshalIndent(d, "", "  ")
	var n strings.Builder
	if len(notes) == 0 {
		n.WriteString("- all referenced files and symbols were found\n")
	}
	for i, s := range notes {
		if i == 30 {
			fmt.Fprintf(&n, "- … %d more\n", len(notes)-30)
			break
		}
		n.WriteString("- " + s + "\n")
	}
	return fmt.Sprintf(`You review a description of the data flows of one part of the PHP project %q. Another agent wrote it; check every claim against the code.

Unit: %s (directory %s)
%s

Tiegel checked the references mechanically:
%s
Check: do the files and symbols exist and do what the step says? Is the order of the steps right? Are important flows missing (entry points, persistence, external calls, events)? Remove what you cannot confirm in the code, fix what is wrong, add what is missing. Move doubts to openQuestions.

The description:
%s

Answer with exactly one fenced json block:
%s
`, project, u.Title, u.Dir, readOnlyRules, n.String(), "```json\n"+string(b)+"\n```",
		"```json\n{\n  \"confidence\": 0.0-1.0 (how well the corrected description matches the code),\n  \"issues\": [\"what you corrected, one line each\"],\n  \"description\": { the corrected description in the same shape }\n}\n```")
}

// WritePrompt asks the third agent for the Markdown document.
func WritePrompt(project string, u Unit, d Description) string {
	b, _ := json.MarshalIndent(d, "", "  ")
	return fmt.Sprintf(`Write the knowledge base page for one part of the PHP project %q from the validated description below. Do not use tools; everything you need is here.

Unit: %s

Format (Markdown, compact, no front matter, no title line – Tiegel adds both):
## Overview
2-4 sentences.
## Data flows
One "### <flow name>" per flow: a line "**Trigger:** …", then a numbered list of steps, each "[path:line](path#Lline) `+"`Symbol`"+` – what happens", then "**Data:** …" and "**Side effects:** …" when known.
## Entities
Bullet list "**Name** ([path](path)): one line".
## External
Bullet list "**Name** (kind): one line".
## Open questions
Bullet list; leave the section out when there are none.

Use only facts from the description; keep paths project relative exactly as given. Steps marked "verified": false could not be confirmed in the code – mark them "*(unverified)*".

The description:
%s
`, project, u.Title, "```json\n"+string(b)+"\n```")
}

// RepairPrompt asks an agent to fix an answer that did not parse.
func RepairPrompt(err error) string {
	return fmt.Sprintf(`Your answer could not be used: %v.
Answer again with exactly one fenced json block in the requested shape, nothing else.`, err)
}

// RepairMarkdownPrompt asks the writer to fix its Markdown.
func RepairMarkdownPrompt(err error) string {
	return fmt.Sprintf(`Your page could not be used: %v.
Answer again with the complete page in the requested format (## Overview, ## Data flows, …), nothing else.`, err)
}
