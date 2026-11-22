package sonic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
)

// Spec describes an operation. Its input and output come from the type parameters of
// Define, so only the words live here.
type Spec struct {
	Name        string
	Description string
	// Plain-language instructions for the agent. Nobody scripts the clicks.
	Instructions string
}

// Definition is the JSON document the compute-box stores for an operation.
type Definition struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Instructions string             `json:"instructions"`
	Input        *jsonschema.Schema `json:"input"`
	Output       *jsonschema.Schema `json:"output"`
}

// Operation is a typed task the agent carries out: I in, O out.
type Operation[I, O any] struct {
	project    *Project
	Definition Definition
}

// Define declares an operation on a project. I and O are plain structs; their JSON tags
// and `jsonschema` tags become the input and output schemas.
func Define[I, O any](p *Project, spec Spec) *Operation[I, O] {
	for _, d := range p.ops {
		if d.Name == spec.Name {
			panic(fmt.Sprintf("sonic: operation %s is declared twice in %s", spec.Name, p.Name))
		}
	}
	instructions := strings.TrimSpace(dedent(spec.Instructions))
	if p.Context != "" {
		instructions = p.Context + "\n\n" + instructions
	}
	def := Definition{
		Name:         spec.Name,
		Description:  spec.Description,
		Instructions: instructions,
		Input:        schemaOf[I](),
		Output:       schemaOf[O](),
	}
	p.ops = append(p.ops, def)
	return &Operation[I, O]{project: p, Definition: def}
}

// RunOption adjusts a single call.
type RunOption func(*runOptions)

type runOptions struct {
	machine        string
	idempotencyKey string
	onStep         func(Step)
}

// OnMachine runs the job on a machine other than the project's default.
func OnMachine(name string) RunOption { return func(o *runOptions) { o.machine = name } }

// IdempotencyKey makes retries safe: the same key returns the original job.
func IdempotencyKey(key string) RunOption { return func(o *runOptions) { o.idempotencyKey = key } }

// OnStep is called for each action the agent takes.
func OnStep(fn func(Step)) RunOption { return func(o *runOptions) { o.onStep = fn } }

// Run carries out the operation and returns its typed result. The compute-box checks the
// input against the schema before any click, and the result is decoded into O.
func (op *Operation[I, O]) Run(ctx context.Context, input I, opts ...RunOption) (O, error) {
	var out O
	var o runOptions
	for _, opt := range opts {
		opt(&o)
	}
	raw, jobID, err := op.project.runJob(ctx, op.Definition.Name, input, o)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, &Error{Code: "output_invalid", Message: op.Definition.Name + ": " + err.Error(), JobID: jobID}
	}
	return out, nil
}

func schemaOf[T any]() *jsonschema.Schema {
	r := &jsonschema.Reflector{DoNotReference: true, ExpandedStruct: true}
	s := r.Reflect(new(T))
	s.Version = ""
	s.ID = ""
	return s
}

// dedent strips the common indentation of a raw-string block, so instructions can be
// written indented in source.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}
