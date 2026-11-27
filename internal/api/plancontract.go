package api

import "time"

// Plan mode contract (D567–D570): the planmode module orchestrates
// several coding agent sessions (roles) around one working document per
// plan. Namespace plan.*: plan.start, plan.status, plan.list,
// plan.document, plan.accept, plan.requestChanges, plan.discard,
// plan.execute, plan.resolve, plan.cancel, plan.resume, plan.transcript,
// plan.changes, plan.diff, plan.revert, plan.apply, plan.keepBranch,
// plan.propose (MCP inbox). The types below are what clients (GUI,
// headless, serve) program against; the orchestration itself lives in
// internal/orchestrate.

// Topics of the plan namespace.
const (
	// TopicPlanProgress: phase, roles or document of a plan changed
	// (PlanStatus).
	TopicPlanProgress = "plan.progress"
	// TopicPlanActivity: a role session streamed something
	// (PlanActivity).
	TopicPlanActivity = "plan.activity"
)

// Plan states (PlanStatus.State).
const (
	PlanDrafting   = "drafting"
	PlanReview     = "awaiting-acceptance"
	PlanAccepted   = "accepted"
	PlanExecuting  = "executing"
	PlanNeedsUser  = "needs-user"
	PlanDone       = "done"
	PlanIncomplete = "incomplete"
	PlanFailed     = "failed"
	PlanCancelled  = "cancelled"
	PlanDiscarded  = "discarded"
	PlanApplied    = "applied"
)

// Phases (PlanStatus.Phase).
const (
	PhaseDraft      = "draft"
	PhaseValidate   = "validate"
	PhaseQAReview   = "qa-review"
	PhaseSummary    = "summary"
	PhaseAcceptance = "acceptance"
	PhaseImplement  = "implement"
	PhaseCritic     = "critic"
	PhaseRate       = "rate"
	PhaseGates      = "gates"
	PhaseDebate     = "debate"
	PhaseFinal      = "final"
)

// Roles of plan sessions.
const (
	RolePlannerArch  = "planner-architecture"
	RolePlannerCode  = "planner-code"
	RolePlannerTests = "planner-tests"
	RoleValidator    = "validator"
	RoleQA           = "qa"
	RoleDev          = "dev"
	RoleCritic       = "critic"
	RoleMediator     = "mediator"
	RoleSummarizer   = "summarizer"
)

// Mediator modes (planmode.mediator).
const (
	MediatorOff    = "off"
	MediatorAuto   = "auto"
	MediatorAlways = "always"
)

// Finding states.
const (
	FindingVerified = "verified"
	FindingRefuted  = "refuted"
	FindingUnknown  = "unknown"
)

// Review item states and severities.
const (
	ReviewOpen      = "open"
	ReviewAccepted  = "accepted"
	ReviewRejected  = "rejected"
	ReviewNeedsInfo = "needs-info"
	ReviewResolved  = "resolved"
	ReviewDisputed  = "disputed"
	ReviewWithdrawn = "withdrawn"

	SeverityBlocker = "blocker"
	SeverityMajor   = "major"
	SeverityMinor   = "minor"
)

// Review targets: what a review item is about.
const (
	TargetPlan  = "plan"
	TargetImpl  = "impl"
	TargetTests = "tests"
)

// PlanFinding is a claim about the code with its evidence.
type PlanFinding struct {
	ID    string `json:"id"`
	Claim string `json:"claim"`
	// Evidence is "file:line" (project relative).
	Evidence string `json:"evidence,omitempty"`
	// Status is verified, refuted or unknown.
	Status string `json:"status"`
	// Note explains the status (validator, mechanical check).
	Note string `json:"note,omitempty"`
	By   string `json:"by,omitempty"`
}

// PlanStep is an implementation step.
type PlanStep struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Detail string   `json:"detail,omitempty"`
	Files  []string `json:"files,omitempty"`
	// Status is open, done or partial (reported by DEV, checked by the
	// gates).
	Status string `json:"status,omitempty"`
	Note   string `json:"note,omitempty"`
}

// PlanTest is a test case of the test plan.
type PlanTest struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Given string `json:"given,omitempty"`
	When  string `json:"when,omitempty"`
	Then  string `json:"then,omitempty"`
	// File is the target test file (project relative).
	File string `json:"file,omitempty"`
	// Kind is "new" (must fail on the base code) or "regression"
	// (passes before and after).
	Kind string `json:"kind,omitempty"`
	// Tests are the test IDs QA wrote for this case ("Ns\Cls::method").
	Tests []string `json:"tests,omitempty"`
	// Status is the last result: passed, failed, missing.
	Status string `json:"status,omitempty"`
}

// PlanCriterion is an acceptance criterion mapped to test cases.
type PlanCriterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Tests are IDs of PlanTest.
	Tests []string `json:"tests"`
	// Met is set when every mapped test passed.
	Met bool `json:"met,omitempty"`
}

// PlanRisk is a risk or open question.
type PlanRisk struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Kind is risk or question.
	Kind string `json:"kind,omitempty"`
}

// PlanResponse is a role's answer to a review item.
type PlanResponse struct {
	Role string `json:"role"`
	// Verdict is accept, reject or needs-info (or insist / withdraw from
	// the raiser).
	Verdict  string    `json:"verdict"`
	Text     string    `json:"text,omitempty"`
	Evidence string    `json:"evidence,omitempty"`
	Round    int       `json:"round,omitempty"`
	Time     time.Time `json:"time"`
}

// PlanDecision is how a dispute was decided.
type PlanDecision struct {
	// By is mediator or user.
	By string `json:"by"`
	// Winner is the role that is right (dev or qa), or "" for a comment.
	Winner string    `json:"winner,omitempty"`
	Reason string    `json:"reason,omitempty"`
	Time   time.Time `json:"time"`
}

// PlanReviewItem is an entry of the review log: a finding of a role, the
// critic or a mechanical gate, with the debate about it.
type PlanReviewItem struct {
	ID string `json:"id"`
	// From is the raising role (qa, dev, critic, validator, gate:<name>,
	// runner, user); To the role that must answer (dev or qa).
	From     string `json:"from"`
	To       string `json:"to"`
	Target   string `json:"target"`
	Severity string `json:"severity"`
	Claim    string `json:"claim"`
	Evidence string `json:"evidence,omitempty"`
	// Suggestion is what the raiser proposes.
	Suggestion string         `json:"suggestion,omitempty"`
	Status     string         `json:"status"`
	Round      int            `json:"round,omitempty"`
	Responses  []PlanResponse `json:"responses,omitempty"`
	Decision   *PlanDecision  `json:"decision,omitempty"`
	// Mechanical marks items of Tiegel's own gates (not negotiable by the
	// agents; the user can overrule them).
	Mechanical bool `json:"mechanical,omitempty"`
}

// PlanDecisionLog is a decision recorded in the document.
type PlanDecisionLog struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	By   string    `json:"by"`
	Time time.Time `json:"time"`
}

// PlanGate is the result of a mechanical gate.
type PlanGate struct {
	// Name is criteria, hollow, red, mutation, coverage, unfinished or
	// tests.
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	// Skipped explains why the gate did not run.
	Skipped string `json:"skipped,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// PlanComment is a change request of the user.
type PlanComment struct {
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

// PlanDocument is the working document of a plan. Its Markdown form is
// rendered by Tiegel (<config>/projects/<hash>/plans/<id>.md); agents
// never get the whole document, only budgeted sections.
type PlanDocument struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	Phase     string            `json:"phase"`
	Created   time.Time         `json:"created"`
	Updated   time.Time         `json:"updated"`
	Revision  int               `json:"revision"`
	Goal      string            `json:"goal"`
	Context   string            `json:"context,omitempty"`
	Refs      []string          `json:"refs,omitempty"`
	Findings  []PlanFinding     `json:"findings,omitempty"`
	Steps     []PlanStep        `json:"steps,omitempty"`
	Tests     []PlanTest        `json:"tests,omitempty"`
	Criteria  []PlanCriterion   `json:"criteria,omitempty"`
	Risks     []PlanRisk        `json:"risks,omitempty"`
	Review    []PlanReviewItem  `json:"review,omitempty"`
	Decisions []PlanDecisionLog `json:"decisions,omitempty"`
	Gates     []PlanGate        `json:"gates,omitempty"`
	Comments  []PlanComment     `json:"comments,omitempty"`
	Summary   string            `json:"summary,omitempty"`
}

// PlanRole is the state of one role session.
type PlanRole struct {
	Role string `json:"role"`
	// State is idle, running, done or failed.
	State   string    `json:"state"`
	Task    string    `json:"task,omitempty"`
	Started time.Time `json:"started,omitempty"`
	// ElapsedMs is the time of the running or last turn; TotalMs all
	// turns.
	ElapsedMs int64  `json:"elapsedMs,omitempty"`
	TotalMs   int64  `json:"totalMs,omitempty"`
	Turns     int    `json:"turns,omitempty"`
	Session   string `json:"session,omitempty"`
	Error     string `json:"error,omitempty"`
}

// PlanMetrics make the orchestration measurable (mediator validation,
// D570).
type PlanMetrics struct {
	Mediator          string `json:"mediator"`
	Subagents         bool   `json:"subagents"`
	Rounds            int    `json:"rounds"`
	Iterations        int    `json:"iterations"`
	IterationsToGreen int    `json:"iterationsToGreen,omitempty"`
	FindingsRaised    int    `json:"findingsRaised"`
	FindingsAccepted  int    `json:"findingsAccepted"`
	FindingsRejected  int    `json:"findingsRejected"`
	MediatorDecisions int    `json:"mediatorDecisions"`
	UserOverrides     int    `json:"userOverrides"`
	Turns             int    `json:"turns"`
	Repairs           int    `json:"repairs"`
	// PromptTokens and ReplyTokens are estimates (characters / 4).
	PromptTokens int   `json:"promptTokens"`
	ReplyTokens  int   `json:"replyTokens"`
	DurationMs   int64 `json:"durationMs"`
	TestRuns     int   `json:"testRuns"`
	Blocked      int   `json:"blocked"`
}

// PlanWorkspace says where the execution happens.
type PlanWorkspace struct {
	// Mode is worktree or in-place.
	Mode string `json:"mode"`
	// Dir is the project directory the agents work in.
	Dir    string `json:"dir"`
	Branch string `json:"branch,omitempty"`
	// Base is the commit the plan branch started from (a snapshot
	// commit when the working tree was dirty).
	Base string `json:"base,omitempty"`
	// Note explains fallbacks (in-place, snapshot of local changes).
	Note string `json:"note,omitempty"`
}

// PlanStatus is the result of plan.status and the plan.progress event.
type PlanStatus struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
	Phase string `json:"phase"`
	// Running is set while an orchestration runs for the plan.
	Running   bool       `json:"running,omitempty"`
	Iteration int        `json:"iteration,omitempty"`
	Round     int        `json:"round,omitempty"`
	Roles     []PlanRole `json:"roles,omitempty"`
	// Disputes are review items waiting for the user.
	Disputes []PlanReviewItem `json:"disputes,omitempty"`
	// Open counts open review items.
	Open      int            `json:"open,omitempty"`
	Gates     []PlanGate     `json:"gates,omitempty"`
	Metrics   PlanMetrics    `json:"metrics"`
	Workspace *PlanWorkspace `json:"workspace,omitempty"`
	// Document is the path of the Markdown working document.
	Document string    `json:"document"`
	Started  time.Time `json:"started,omitempty"`
	Error    string    `json:"error,omitempty"`
	// Notes are remarks for the user (fallbacks, skipped gates).
	Notes []string `json:"notes,omitempty"`
}

// PlanActivity is the payload of plan.activity.
type PlanActivity struct {
	Plan  string     `json:"plan"`
	Role  string     `json:"role"`
	Entry AgentEntry `json:"entry"`
	Delta string     `json:"delta,omitempty"`
}

// PlanStartParams are the params of plan.start.
type PlanStartParams struct {
	Goal string `json:"goal"`
	// Subagents overrides planmode.subagents; Mediator planmode.mediator.
	Subagents *bool  `json:"subagents,omitempty"`
	Mediator  string `json:"mediator,omitempty"`
	// Wait blocks until the planning phase ended.
	Wait bool `json:"wait,omitempty"`
}

// PlanIDParams address a plan ("" = the latest).
type PlanIDParams struct {
	ID string `json:"id,omitempty"`
}

// PlanDocumentParams are the params of plan.document.
type PlanDocumentParams struct {
	ID string `json:"id,omitempty"`
	// Section returns only one section's Markdown (goal, context,
	// findings, plan, tests, criteria, risks, review, decisions, gates,
	// summary).
	Section string `json:"section,omitempty"`
}

// PlanDocumentResult is the result of plan.document.
type PlanDocumentResult struct {
	Path     string       `json:"path"`
	Markdown string       `json:"markdown"`
	Document PlanDocument `json:"document"`
	// History are the revision files (oldest first).
	History []string `json:"history,omitempty"`
}

// PlanCommentParams are the params of plan.accept and
// plan.requestChanges.
type PlanCommentParams struct {
	ID      string `json:"id,omitempty"`
	Comment string `json:"comment,omitempty"`
	Wait    bool   `json:"wait,omitempty"`
}

// PlanExecuteParams are the params of plan.execute and plan.resume.
type PlanExecuteParams struct {
	ID string `json:"id,omitempty"`
	// Wait blocks until the execution ended (or needs the user).
	Wait bool `json:"wait,omitempty"`
	// AllowShell answers shell commands whose agent setting is "ask"
	// with allow (headless --yes); otherwise they are rejected.
	AllowShell bool `json:"allowShell,omitempty"`
}

// PlanResolveParams are the params of plan.resolve.
type PlanResolveParams struct {
	ID string `json:"id,omitempty"`
	// Item is the review item.
	Item string `json:"item"`
	// Decision is dev, qa (who is right), dismiss (drop the item) or
	// comment.
	Decision string `json:"decision"`
	Comment  string `json:"comment,omitempty"`
}

// PlanTranscriptParams are the params of plan.transcript.
type PlanTranscriptParams struct {
	ID   string `json:"id,omitempty"`
	Role string `json:"role"`
}

// PlanChange is a file changed by the plan execution.
type PlanChange struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Test    bool   `json:"test,omitempty"`
}

// PlanDiffParams are the params of plan.diff and plan.revert.
type PlanDiffParams struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

// PlanDiff is a file before the plan and now.
type PlanDiff struct {
	PlanChange
	Original string `json:"original"`
	Current  string `json:"current"`
	Unified  string `json:"unified"`
}

// PlanProposeParams are the params of plan.propose: an MCP tool of a
// role session proposes a change of the document; Tiegel merges it after
// the role's turn.
type PlanProposeParams struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	// Op is update (Section, Content), finding (Claim, Evidence) or
	// comment (Item, Content).
	Op       string `json:"op"`
	Section  string `json:"section,omitempty"`
	Content  string `json:"content,omitempty"`
	Claim    string `json:"claim,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	Item     string `json:"item,omitempty"`
}
