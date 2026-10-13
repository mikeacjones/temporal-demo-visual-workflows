package patterns

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

func TestPatternsAreCompleteAndTheirFlowsValid(t *testing.T) {
	all := All("http://sandbox")
	require.Len(t, all, 35, "one entry per pattern in Temporal's catalog")
	ids := map[string]bool{}
	for _, p := range all {
		t.Run(p.ID, func(t *testing.T) {
			require.False(t, ids[p.ID], "duplicate id")
			ids[p.ID] = true
			require.Contains(t, Categories, p.Category)
			require.True(t, strings.HasPrefix(p.DocsURL, docs))
			require.NotEmpty(t, p.Summary)
			require.NotEmpty(t, p.Build)
			require.Contains(t, []string{Visual, BuiltIn, NotInBuilder}, p.Support)
			if p.Support == Visual {
				require.NotNil(t, p.Flow, "a visual pattern needs an example")
			}
			children := map[string]bool{}
			for _, c := range p.Children {
				require.Empty(t, c.Validate(), c.Name)
				children[c.ID] = true
			}
			if p.Flow == nil {
				return
			}
			require.Empty(t, p.Flow.Validate())
			for _, sub := range p.Flow.SubflowIDs() {
				require.True(t, children[sub], "child flow %s is included", sub)
			}
			for _, step := range p.Build {
				if step.Node != "" {
					require.True(t, slices.ContainsFunc(p.Flow.Nodes, func(n flow.Node) bool { return n.ID == step.Node }), "step refers to node %q", step.Node)
				}
			}
		})
	}
	for _, p := range all {
		for _, r := range p.Related {
			require.True(t, ids[r], "%s relates to unknown %s", p.ID, r)
		}
	}
}
