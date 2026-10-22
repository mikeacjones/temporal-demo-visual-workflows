package flow

import (
	"fmt"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
)

// A saga (the Saga step) groups steps on the main path whose work can be
// undone. Each member says two things:
//
//   - whether its failure unwinds the saga (Unwinds), or is handled: the
//     run carries on past it;
//   - how it is compensated, if at all: by a compensation step (the first of
//     a short chain that runs only when the saga unwinds), or by its
//     connector operation's built-in rollback.
//
// A member's compensation registers when the member completes, like a defer
// in Go. When an unwinding member fails, the registered compensations run
// newest first; then the Saga takes its compensated outcome, or failed if a
// compensation failed. With nothing connected there, the run fails.
// Steps that aren't members never unwind a saga and are never compensated.

// TypeSaga is the Saga step. It is an entry: its outcomes run only once the
// saga has unwound.
const TypeSaga NodeType = "saga"

// The Saga step's outcomes.
const (
	HandleCompensated = "compensated"
	HandleFailed      = "failed"
)

// SagaConfig lists a saga's members.
type SagaConfig struct {
	Members []SagaMember `json:"members,omitempty"`
}

// SagaMember is one step's part in a saga.
type SagaMember struct {
	Step string `json:"step"`
	// Unwinds: the step's failure unwinds the saga. Otherwise its failure is
	// handled: it takes its error outcome if it has one, and otherwise the
	// run carries on along its main outcome.
	Unwinds bool `json:"unwinds"`
	// Compensation is the step that undoes this one: the first step of a
	// chain that runs only when the saga unwinds, and reads this step's
	// output as usual (steps.<key>).
	Compensation string `json:"compensation,omitempty"`
	// BuiltIn compensates with the connector operation's built-in rollback.
	BuiltIn bool `json:"builtIn,omitempty"`
}

// SagaRole is a step's membership of a saga.
type SagaRole struct {
	Saga string
	SagaMember
}

// SagaRoles returns every member step's role, by step ID.
func (d *Definition) SagaRoles() map[string]SagaRole {
	roles := map[string]SagaRole{}
	for _, n := range d.Nodes {
		if n.Type != TypeSaga {
			continue
		}
		c, _ := DecodeConfig[SagaConfig](n)
		for _, m := range c.Members {
			if _, taken := roles[m.Step]; !taken {
				roles[m.Step] = SagaRole{Saga: n.ID, SagaMember: m}
			}
		}
	}
	return roles
}

// CompensationSteps maps each compensation chain's first step to the member
// it compensates.
func (d *Definition) CompensationSteps() map[string]string {
	out := map[string]string{}
	for step, role := range d.SagaRoles() {
		if role.Compensation != "" {
			out[role.Compensation] = step
		}
	}
	return out
}

// sagaProblems checks every saga: members on the main path, outside Loop and
// For each bodies, in one saga each; unwinding members without an error
// outcome of their own; compensations that start a chain of their own, and
// built-in rollbacks only where the operation has one.
func (d *Definition) sagaProblems(byID map[string]Node) []Problem {
	var problems []Problem
	add := func(nodeID, format string, args ...any) {
		problems = append(problems, Problem{NodeID: nodeID, Message: fmt.Sprintf(format, args...)})
	}
	var main map[string]bool
	for _, n := range d.Nodes {
		if n.Type == TypeTrigger {
			main = d.Reachable(n.ID)
		}
	}
	bodies := map[string]bool{}
	for _, n := range d.Nodes {
		if HasBody(n) {
			for id := range d.Body(n.ID) {
				bodies[id] = true
			}
		}
	}
	member := map[string]string{} // step -> its saga
	compensates := map[string]string{}
	chains := map[string]bool{}
	for _, saga := range d.Nodes {
		if saga.Type != TypeSaga {
			continue
		}
		c, err := DecodeConfig[SagaConfig](saga)
		if err != nil {
			continue
		}
		if len(c.Members) == 0 {
			add(saga.ID, "%s has no members: link the steps whose failure unwinds it, or that it undoes", saga.Label)
		}
		for _, m := range c.Members {
			step, ok := byID[m.Step]
			if !ok {
				add(saga.ID, "%s lists a step that no longer exists", saga.Label)
				continue
			}
			if prev, dup := member[m.Step]; dup {
				add(step.ID, "%s is in two sagas, %s and %s; a step belongs to one", step.Label, byID[prev].Label, saga.Label)
				continue
			}
			member[m.Step] = saga.ID
			switch {
			case IsEntry(step.Type) || step.Type == TypeEnd || step.Type == TypeRespond:
				add(step.ID, "%s can't be in a saga", step.Label)
			case !main[step.ID]:
				add(step.ID, "%s is in %s but not on the main path; a saga's steps are on the main path", step.Label, saga.Label)
			case bodies[step.ID]:
				add(step.ID, "%s is inside a Loop or For each body, so it can't be in %s; put the whole loop in a flow and compensate that", step.Label, saga.Label)
			}
			if m.Unwinds {
				for _, e := range d.Edges {
					if e.Source == step.ID && e.Handle() == HandleError {
						add(step.ID, "%s unwinds %s when it fails, so it can't also have an error outcome; remove the error edge, or set it to handled", step.Label, saga.Label)
						break
					}
				}
			}
			if m.BuiltIn {
				_, op, _, err := ConnectorStep(step)
				switch {
				case m.Compensation != "":
					add(step.ID, "%s has both a compensation step and its built-in rollback; choose one", step.Label)
				case err != nil || op.Compensation == nil:
					add(step.ID, "%s has no built-in rollback", step.Label)
				}
			}
			if m.Compensation == "" {
				continue
			}
			comp, ok := byID[m.Compensation]
			switch {
			case !ok:
				add(step.ID, "%s's compensation step no longer exists", step.Label)
				continue
			case compensates[comp.ID] != "":
				add(comp.ID, "%s already compensates %s; each step compensates one", comp.Label, byID[compensates[comp.ID]].Label)
				continue
			}
			compensates[comp.ID] = step.ID
			for id := range d.Reachable(comp.ID) {
				chains[id] = true
				n := byID[id]
				switch n.Type {
				case TypeRespond, TypeEnd, TypeLoop, TypeForEach, TypeSaga:
					add(id, "%s can't be part of a compensation", n.Label)
				}
			}
		}
	}
	for step, saga := range member {
		if chains[step] {
			add(step, "%s compensates a step, so it can't also be in %s", byID[step].Label, byID[saga].Label)
		}
	}
	return problems
}

func (c SagaConfig) validate() error {
	seen := map[string]bool{}
	for _, m := range c.Members {
		if m.Step == "" || seen[m.Step] {
			return fmt.Errorf("each member is a different step")
		}
		seen[m.Step] = true
	}
	return nil
}

// CompensationArgument builds a connector step's built-in rollback: the
// Activity it runs and that Activity's argument, from the step's own argument
// and output. It runs in Workflow code when the step completes.
func CompensationArgument(conn connectors.Connector, op connectors.Operation, arg, out map[string]any) (connectors.Operation, map[string]any, error) {
	comp := op.Compensation
	if comp == nil {
		return connectors.Operation{}, nil, fmt.Errorf("%s has no built-in rollback", op.Label)
	}
	undo, ok := conn.Operation(comp.Operation)
	if !ok {
		return undo, nil, fmt.Errorf("%s's rollback names no operation %q", op.Label, comp.Operation)
	}
	settings := map[string]any{}
	for _, name := range comp.Settings {
		if v, ok := arg[name]; ok {
			settings[name] = Stringify(v)
		}
	}
	mapping := map[string]string{}
	for k, v := range comp.Mapping {
		mapping[k] = v
	}
	built, err := ConnectorArgument(conn, undo, ConnectorConfig{Operation: undo.Name, Settings: settings, Mapping: mapping}, map[string]any{"arg": arg, "out": out})
	if err != nil {
		return undo, nil, fmt.Errorf("%s's rollback: %w", op.Label, err)
	}
	return undo, built, nil
}
