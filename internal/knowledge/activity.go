package knowledge

import "context"

// StepInfo names the item and step an agent session works on (D566).
// The pipeline puts it into the context of Agent.NewSession, so that an
// agent implementation can attribute its live activity (thoughts,
// message chunks, tool calls) to the item.
type StepInfo struct {
	Item, Level, Title, Step string
	// Index is the item's position in the run.
	Index int
}

type stepKey struct{}

// WithStep returns ctx carrying s.
func WithStep(ctx context.Context, s StepInfo) context.Context {
	return context.WithValue(ctx, stepKey{}, s)
}

// StepOf returns the step of ctx (ok false outside the pipeline).
func StepOf(ctx context.Context) (StepInfo, bool) {
	s, ok := ctx.Value(stepKey{}).(StepInfo)
	return s, ok
}

// StepIndex is the position of a step within an item (1..StepCount).
func StepIndex(step string) int {
	switch step {
	case StepDescribe:
		return 1
	case StepValidate:
		return 2
	case StepWrite:
		return 3
	}
	return 0
}

// StepCount is the number of steps per item.
const StepCount = 3
