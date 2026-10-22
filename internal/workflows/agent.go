package workflows

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

type AgentInput struct {
	// Label is the agent step's, for summaries in the event history.
	Label         string           `json:"label,omitempty"`
	Instructions  string           `json:"instructions"`
	Tools         []flow.AgentTool `json:"tools"`
	AllowAskHuman bool             `json:"allowAskHuman"`
	MaxTurns      int              `json:"maxTurns"`
	// Context is the flow data the agent may use: the trigger input, the
	// outputs of earlier steps, and the run's variables.
	Context map[string]any `json:"context"`
	// ModelOptions and ToolOptions are the resolved Activity settings for
	// model calls and tool calls.
	ModelOptions flow.ActivityOptions `json:"modelOptions"`
	ToolOptions  flow.ActivityOptions `json:"toolOptions"`
}

type AgentResult struct {
	Answer string `json:"answer"`
	// Data is the answer parsed as JSON when the agent replied with JSON, so
	// later steps can branch on fields: {{ steps.triage.data.priority }}.
	Data      any `json:"data,omitempty"`
	Turns     int `json:"turns"`
	ToolCalls int `json:"toolCalls"`
}

const agentSystemPrompt = `You are an automation agent running as one step of a workflow.
Use the tools to gather the facts you need instead of guessing. If a tool fails, decide whether another approach can still answer the task.
When you have finished, reply with the final result only. If the instructions ask for JSON, reply with a single JSON object and nothing else.`

const askHumanTool = "ask_human"

// AgentWorkflow is a ReAct loop where every step is durable: each model turn
// is an Activity, each tool call is an Activity, and a question for a person
// is an Update the loop waits on. If the worker crashes mid-task, the agent
// resumes from its last completed step instead of starting over, and paid-for
// model turns are never repeated.
//
// The loop is written out by hand rather than with the SDK's tool runner
// because each iteration has to cross the Workflow/Activity boundary.
func AgentWorkflow(ctx workflow.Context, in AgentInput) (AgentResult, error) {
	var a *Activities
	state := flow.AgentState{Status: "thinking", MaxTurns: in.MaxTurns, Trace: []flow.TraceEntry{}}
	var answer *string

	if err := workflow.SetQueryHandler(ctx, flow.QueryAgent, func() (flow.AgentState, error) {
		return state, nil
	}); err != nil {
		return AgentResult{}, err
	}
	if err := workflow.SetUpdateHandlerWithOptions(ctx, flow.UpdateAnswer,
		func(ctx workflow.Context, text string) error {
			answer = &text
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(text string) error {
			if state.PendingQuestion == "" || answer != nil {
				return fmt.Errorf("the agent has not asked a question")
			}
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("answer is empty")
			}
			return nil
		}},
	); err != nil {
		return AgentResult{}, err
	}
	trace := func(e flow.TraceEntry) {
		e.At = workflow.Now(ctx)
		state.Trace = append(state.Trace, e)
	}

	tools := make([]LLMTool, 0, len(in.Tools)+1)
	byName := map[string]flow.AgentTool{}
	for _, t := range in.Tools {
		byName[t.Name] = t
		props := map[string]any{}
		required := []string{}
		for _, p := range t.Params {
			props[p.Name] = map[string]any{"type": "string", "description": p.Description}
			required = append(required, p.Name)
		}
		tools = append(tools, LLMTool{Name: t.Name, Description: t.Description, Properties: props, Required: required})
	}
	if in.AllowAskHuman {
		tools = append(tools, LLMTool{
			Name:        askHumanTool,
			Description: "Ask the person supervising this workflow a question and wait for their reply. Use it when you need a decision or information no other tool can provide.",
			Properties:  map[string]any{"question": map[string]any{"type": "string", "description": "The question to ask."}},
			Required:    []string{"question"},
		})
	}

	contextJSON, _ := json.MarshalIndent(in.Context, "", "  ")
	first, _ := json.Marshal(map[string]any{
		"role":    "user",
		"content": in.Instructions + "\n\nWorkflow data available to you:\n```json\n" + truncate(string(contextJSON), 20000) + "\n```",
	})
	messages := []json.RawMessage{first}

	// Temporal (not the Anthropic SDK) owns retries for rate limits and
	// server errors, using the retry policy set on the agent step.
	modelOptions, toolOptions := in.ModelOptions, in.ToolOptions
	if modelOptions.StartToCloseSeconds == 0 {
		modelOptions = agentModelDefaults
	}
	if toolOptions.StartToCloseSeconds == 0 {
		toolOptions = flow.SystemActivityDefaults
	}
	result := AgentResult{}

	for turn := 1; turn <= in.MaxTurns; turn++ {
		state.Turn, state.Status = turn, "thinking"
		var resp LLMResponse
		err := workflow.ExecuteActivity(
			workflow.WithActivityOptions(ctx, activityOptions(fmt.Sprintf("turn-%d-model", turn), truncate(fmt.Sprintf("%s · model turn %d", in.Label, turn), maxSummary), modelOptions)),
			a.CallClaude, LLMRequest{System: agentSystemPrompt, Tools: tools, Messages: messages},
		).Get(ctx, &resp)
		if err != nil {
			state.Status = "failed"
			return result, err
		}
		messages = append(messages, resp.Assistant)
		result.Turns = turn
		for _, r := range resp.Reasoning {
			trace(flow.TraceEntry{Kind: "reasoning", Text: r})
		}

		if len(resp.ToolCalls) == 0 {
			trace(flow.TraceEntry{Kind: "final", Text: resp.Text})
			state.Status, state.Answer = "done", resp.Text
			result.Answer = strings.TrimSpace(resp.Text)
			var data any
			if json.Unmarshal([]byte(stripCodeFence(result.Answer)), &data) == nil {
				result.Data = data
			}
			return result, nil
		}
		if resp.Text != "" {
			trace(flow.TraceEntry{Kind: "reasoning", Text: resp.Text})
		}

		// Every tool result for this turn goes back in a single user message.
		var toolResults []map[string]any
		for _, call := range resp.ToolCalls {
			trace(flow.TraceEntry{Kind: "tool_call", Tool: call.Name, Input: call.Input})
			result.ToolCalls++
			var args map[string]any
			_ = json.Unmarshal(call.Input, &args)

			var content string
			isError := false
			switch tool, known := byName[call.Name]; {
			case call.Name == askHumanTool && in.AllowAskHuman:
				question, _ := args["question"].(string)
				trace(flow.TraceEntry{Kind: "question", Text: question})
				state.Status, state.PendingQuestion, answer = "waiting", question, nil
				// Durable wait: the agent can sit here for days, across worker
				// restarts, without holding any resources.
				if err := workflow.Await(ctx, func() bool { return answer != nil }); err != nil {
					return result, err
				}
				content = *answer
				state.Status, state.PendingQuestion = "acting", ""
				trace(flow.TraceEntry{Kind: "answer", Text: content})

			case known:
				state.Status = "acting"
				req := HTTPRequest{Method: tool.Method}
				url, err := flow.RenderString(tool.URL, map[string]any{"args": args})
				if err != nil {
					content, isError = "invalid arguments: "+err.Error(), true
					break
				}
				req.URL = url
				if tool.Method == http.MethodPost || tool.Method == http.MethodPut {
					body, _ := json.Marshal(args)
					req.Body = string(body)
				}
				// After the tool's retries, the failure goes back to the model,
				// which can adapt, rather than failing the whole agent.
				toolCtx := workflow.WithActivityOptions(ctx, activityOptions(fmt.Sprintf("turn-%d-%s", turn, call.ID), truncate(fmt.Sprintf("%s · tool %s · %s", in.Label, call.Name, requestSummary(req.Method, req.URL)), maxSummary), toolOptions))
				var out HTTPResponse
				if err := workflow.ExecuteActivity(toolCtx, a.HTTPRequest, req).Get(ctx, &out); err != nil {
					if temporal.IsCanceledError(err) {
						return result, err
					}
					content, isError = describeError(err), true
				} else {
					content = truncate(flow.Stringify(out.Body), 8000)
				}

			default:
				content, isError = "unknown tool "+call.Name, true
			}

			trace(flow.TraceEntry{Kind: "tool_result", Tool: call.Name, Text: truncate(content, 2000), IsError: isError})
			toolResults = append(toolResults, map[string]any{
				"type": "tool_result", "tool_use_id": call.ID, "content": content, "is_error": isError,
			})
		}
		next, _ := json.Marshal(map[string]any{"role": "user", "content": toolResults})
		messages = append(messages, next)
	}

	state.Status = "failed"
	return result, temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("agent did not finish within %d turns", in.MaxTurns), "AgentTurnLimit", nil)
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	return strings.TrimSpace(strings.TrimSuffix(s, "```"))
}
