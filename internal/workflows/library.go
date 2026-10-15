package workflows

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// Library message names.
const (
	UpdateSaveFlow   = "saveFlow"
	UpdateCreateFlow = "createFlow"
	UpdateDeleteFlow = "deleteFlow"
	QueryFlows       = "flows"
	QueryVersions    = "versions"
)

type LibraryInput struct {
	// Flows carries the library across Continue-As-New. On the first run it
	// holds the example flows a new user starts with.
	Flows map[string]flow.Definition `json:"flows"`
	// History holds earlier versions of each flow, newest last.
	History map[string][]flow.Definition `json:"history,omitempty"`
	// IdleTimeoutSeconds ends the library after this long without a save.
	// 0 keeps it forever (signed-in users); anonymous sessions use 30 days.
	IdleTimeoutSeconds int `json:"idleTimeoutSeconds,omitempty"`
}

// keptVersions is how many earlier versions of a flow the library keeps.
const keptVersions = 20

// LibraryWorkflow is an entity Workflow holding one user's saved flows and
// their recent versions. Saves are Updates whose validator rejects an invalid
// flow before anything is written to history; reads are Queries. Temporal is
// the only store, so the API stays stateless and every user's flows are
// isolated by Workflow ID.
func LibraryWorkflow(ctx workflow.Context, in LibraryInput) error {
	flows, history := in.Flows, in.History
	if flows == nil {
		flows = map[string]flow.Definition{}
	}
	if history == nil {
		history = map[string][]flow.Definition{}
	}
	writes := 0

	if err := workflow.SetQueryHandler(ctx, QueryFlows, func() ([]flow.Definition, error) {
		list := make([]flow.Definition, 0, len(flows))
		for _, f := range flows {
			list = append(list, f)
		}
		slices.SortFunc(list, func(a, b flow.Definition) int {
			return cmp.Or(b.UpdatedAt.Compare(a.UpdatedAt), cmp.Compare(a.ID, b.ID))
		})
		return list, nil
	}); err != nil {
		return err
	}
	// Versions returns a flow's earlier versions, newest first.
	if err := workflow.SetQueryHandler(ctx, QueryVersions, func(id string) ([]flow.Definition, error) {
		list := slices.Clone(history[id])
		slices.Reverse(list)
		return list, nil
	}); err != nil {
		return err
	}

	save := func(ctx workflow.Context, def flow.Definition) (flow.Definition, error) {
		if prev, ok := flows[def.ID]; ok {
			history[def.ID] = append(history[def.ID], prev)
			if n := len(history[def.ID]); n > keptVersions {
				history[def.ID] = history[def.ID][n-keptVersions:]
			}
		}
		def.Version = flows[def.ID].Version + 1
		def.UpdatedAt = workflow.Now(ctx)
		flows[def.ID] = def
		writes++
		return def, nil
	}
	validate := func(def flow.Definition) error {
		if def.ID == "" {
			return fmt.Errorf("flow needs an id")
		}
		if problems := def.Validate(); len(problems) > 0 {
			return temporal.NewApplicationError("flow is invalid", "InvalidFlow", problems)
		}
		return nil
	}
	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateSaveFlow, save,
		workflow.UpdateHandlerOptions{Validator: validate},
	); err != nil {
		return err
	}
	// Create is a save that refuses an ID already in use. The UI names a new
	// flow itself, so it can open the editor before the library answers; this
	// keeps a reused ID from overwriting a flow.
	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateCreateFlow, save,
		workflow.UpdateHandlerOptions{Validator: func(def flow.Definition) error {
			if _, taken := flows[def.ID]; taken {
				return temporal.NewApplicationError(fmt.Sprintf("flow %q already exists", def.ID), "FlowExists")
			}
			return validate(def)
		}},
	); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateDeleteFlow,
		func(ctx workflow.Context, id string) error {
			delete(flows, id)
			delete(history, id)
			writes++
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(id string) error {
			if _, ok := flows[id]; !ok {
				return fmt.Errorf("flow %q not found", id)
			}
			return nil
		}},
	); err != nil {
		return err
	}

	for {
		seen := writes
		changed := func() bool { return writes != seen || workflow.GetInfo(ctx).GetContinueAsNewSuggested() }
		if in.IdleTimeoutSeconds > 0 {
			active, err := workflow.AwaitWithTimeout(ctx, time.Duration(in.IdleTimeoutSeconds)*time.Second, changed)
			if err != nil {
				return err
			}
			if !active {
				// Ending the Workflow is the cleanup: there is no other store
				// to sweep.
				workflow.GetLogger(ctx).Info("session idle; library closed", "flows", len(flows))
				return nil
			}
		} else if err := workflow.Await(ctx, changed); err != nil {
			return err
		}
		// Every save adds the whole definition to history. When Temporal
		// suggests it (its suggestion weighs history size as well as length),
		// start fresh, carrying the flows and versions forward.
		if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
				return err
			}
			return workflow.NewContinueAsNewError(ctx, LibraryWorkflow, LibraryInput{
				Flows: flows, History: history, IdleTimeoutSeconds: in.IdleTimeoutSeconds,
			})
		}
	}
}

// Template gallery message names.
const (
	UpdatePublish   = "publish"
	UpdateUnpublish = "unpublish"
	QueryTemplates  = "templates"
)

// Template is a flow someone shared. Listed templates appear in the gallery;
// unlisted ones are reachable only with their share link.
type Template struct {
	ID          string          `json:"id"`
	Flow        flow.Definition `json:"flow"`
	Author      string          `json:"author"`
	AuthorKey   string          `json:"authorKey"`
	Listed      bool            `json:"listed"`
	PublishedAt time.Time       `json:"publishedAt"`
}

type GalleryInput struct {
	Templates map[string]Template `json:"templates"`
}

// GalleryWorkflow holds the templates people have shared on one deployment.
// Like the library, it is an entity Workflow: Updates publish and unpublish,
// a Query lists, and Continue-As-New keeps history small. Publishing copies a
// snapshot, so later edits to the author's flow don't change the template.
func GalleryWorkflow(ctx workflow.Context, in GalleryInput) error {
	templates := in.Templates
	if templates == nil {
		templates = map[string]Template{}
	}
	writes := 0
	if err := workflow.SetQueryHandler(ctx, QueryTemplates, func() ([]Template, error) {
		list := make([]Template, 0, len(templates))
		for _, t := range templates {
			list = append(list, t)
		}
		slices.SortFunc(list, func(a, b Template) int {
			return cmp.Or(b.PublishedAt.Compare(a.PublishedAt), cmp.Compare(a.ID, b.ID))
		})
		return list, nil
	}); err != nil {
		return err
	}
	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdatePublish,
		func(ctx workflow.Context, t Template) (Template, error) {
			t.PublishedAt = workflow.Now(ctx)
			templates[t.ID] = t
			writes++
			return t, nil
		},
		workflow.UpdateHandlerOptions{Validator: func(t Template) error {
			if t.ID == "" || t.AuthorKey == "" {
				return fmt.Errorf("template needs an id and an author")
			}
			if problems := t.Flow.Validate(); len(problems) > 0 {
				return fmt.Errorf("flow is invalid: %s", problems[0].Message)
			}
			return nil
		}},
	); err != nil {
		return err
	}
	// Unpublish takes [id, authorKey]: only the author can remove a template.
	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateUnpublish,
		func(ctx workflow.Context, id, authorKey string) error {
			delete(templates, id)
			writes++
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(id, authorKey string) error {
			t, ok := templates[id]
			if !ok || t.AuthorKey != authorKey {
				return fmt.Errorf("template %q not found", id)
			}
			return nil
		}},
	); err != nil {
		return err
	}
	for {
		seen := writes
		if err := workflow.Await(ctx, func() bool {
			return writes != seen || workflow.GetInfo(ctx).GetContinueAsNewSuggested()
		}); err != nil {
			return err
		}
		if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
				return err
			}
			return workflow.NewContinueAsNewError(ctx, GalleryWorkflow, GalleryInput{Templates: templates})
		}
	}
}
