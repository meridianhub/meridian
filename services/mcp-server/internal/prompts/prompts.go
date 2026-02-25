// Package prompts implements MCP prompt definitions for the Meridian MCP server.
// Prompts are guided workflow starters: each prompt returns a list of messages
// (role + content) that prime the LLM for a specific task.
//
// Available prompts:
//   - design-economy    — guided manifest creation workflow
//   - audit-transaction — investigate a transaction's causation tree
//   - simulate-change   — test a manifest change before applying
//   - debug-saga        — diagnose a failed saga execution
package prompts

import (
	"errors"
	"fmt"
	"strings"
)

// ErrPromptNotFound is returned when the requested prompt name is not registered.
var ErrPromptNotFound = errors.New("prompt not found")

// ErrMissingRequiredArgument is returned when a required argument is absent.
var ErrMissingRequiredArgument = errors.New("missing required argument")

// Argument describes a single parameter accepted by a prompt.
type Argument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt describes a registered prompt and its accepted arguments.
type Prompt struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Arguments   []Argument `json:"arguments,omitempty"`
}

// MessageContent holds the text payload of a prompt message.
type MessageContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Message is a single role+content pair in a prompt result.
type Message struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

// GetResult is the payload returned by prompts/get.
type GetResult struct {
	Description string    `json:"description,omitempty"`
	Messages    []Message `json:"messages"`
}

// promptDef holds the static definition and a factory function for building messages.
type promptDef struct {
	meta    Prompt
	builder func(args map[string]string) (*GetResult, error)
}

// Registry holds all registered prompts and dispatches Get calls.
type Registry struct {
	defs []promptDef
}

// NewRegistry creates a Registry pre-loaded with all built-in Meridian prompts.
func NewRegistry() *Registry {
	r := &Registry{}
	r.register()
	return r
}

func (r *Registry) register() {
	r.defs = []promptDef{
		buildDesignEconomy(),
		buildAuditTransaction(),
		buildSimulateChange(),
		buildDebugSaga(),
	}
}

// List returns all registered prompt descriptors (without message content).
// Each Prompt is a deep copy; callers cannot mutate registry state.
func (r *Registry) List() []Prompt {
	result := make([]Prompt, len(r.defs))
	for i, d := range r.defs {
		p := d.meta
		if len(p.Arguments) > 0 {
			args := make([]Argument, len(p.Arguments))
			copy(args, p.Arguments)
			p.Arguments = args
		}
		result[i] = p
	}
	return result
}

// Get returns the message list for the named prompt, substituting the provided
// arguments into templates. Returns ErrPromptNotFound or ErrMissingRequiredArgument
// on failure.
func (r *Registry) Get(name string, args map[string]string) (*GetResult, error) {
	for _, d := range r.defs {
		if d.meta.Name == name {
			return d.builder(args)
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrPromptNotFound, name)
}

// ---- prompt builders ----

func buildDesignEconomy() promptDef {
	return promptDef{
		meta: Prompt{
			Name:        "design-economy",
			Description: "Guided workflow for creating a new Meridian economy manifest. Asks clarifying questions about instruments, account types, sagas, and valuation rules, then generates a complete manifest.",
			Arguments:   []Argument{},
		},
		builder: func(_ map[string]string) (*GetResult, error) {
			return &GetResult{
				Description: "Guided manifest creation workflow",
				Messages: []Message{
					{
						Role: "user",
						Content: MessageContent{
							Type: "text",
							Text: strings.TrimSpace(`
I want to design a new Meridian economy manifest. Please guide me through the process.

Start by asking me about:
1. The types of financial instruments my economy uses (currencies, commodities, energy units, carbon credits, etc.)
2. The account types I need and their normal balances (DEBIT or CREDIT)
3. The saga workflows I want to support (e.g., deposits, withdrawals, transfers, payments)
4. Any valuation rules between instruments (e.g., FX rates, energy pricing)
5. Payment rails I need to connect to (e.g., SEPA, Faster Payments, internal)

After gathering this information, generate a complete manifest in YAML format that I can apply with the meridian_apply_manifest tool.
`),
						},
					},
				},
			}, nil
		},
	}
}

func buildAuditTransaction() promptDef {
	return promptDef{
		meta: Prompt{
			Name:        "audit-transaction",
			Description: "Investigate a specific transaction's causation tree: find the originating saga, its steps, position movements, journal entries, and any compensation actions.",
			Arguments: []Argument{
				{
					Name:        "transaction_id",
					Description: "The transaction ID (UUID or external reference) to audit.",
					Required:    true,
				},
			},
		},
		builder: func(args map[string]string) (*GetResult, error) {
			txnID := args["transaction_id"]
			if txnID == "" {
				return nil, fmt.Errorf("%w: transaction_id", ErrMissingRequiredArgument)
			}
			return &GetResult{
				Description: fmt.Sprintf("Audit transaction %s", txnID),
				Messages: []Message{
					{
						Role: "user",
						Content: MessageContent{
							Type: "text",
							Text: fmt.Sprintf(strings.TrimSpace(`
Please perform a complete audit of transaction %s.

Use the available tools to investigate:

1. **Find the saga execution**: Use meridian_audit_saga_execution to locate the saga that created this transaction and examine its execution steps.

2. **Trace position movements**: Use meridian_audit_position_movements to find all DEBIT and CREDIT entries associated with this transaction.

3. **Check journal entries**: Use meridian_audit_journal_entries to verify the double-entry accounting is balanced.

4. **Check for compensation**: Determine if any compensation actions were triggered and whether they completed successfully.

5. **Summarize findings**: Provide a clear summary of what happened, including:
   - The saga name and trigger that initiated the transaction
   - All accounts affected and the direction of movement
   - Whether the transaction completed successfully or was compensated
   - Any anomalies or errors found

Transaction ID: %s
`), txnID, txnID),
						},
					},
				},
			}, nil
		},
	}
}

func buildSimulateChange() promptDef {
	return promptDef{
		meta: Prompt{
			Name:        "simulate-change",
			Description: "Test a proposed manifest change before applying it. Analyses the impact on existing sagas, instruments, and account types, and identifies potential breaking changes.",
			Arguments: []Argument{
				{
					Name:        "change_description",
					Description: "A description of the manifest change to simulate (e.g., 'add a new instrument CARBON_CREDIT' or 'deprecate the LEGACY_RAIL payment rail').",
					Required:    true,
				},
			},
		},
		builder: func(args map[string]string) (*GetResult, error) {
			change := args["change_description"]
			if change == "" {
				return nil, fmt.Errorf("%w: change_description", ErrMissingRequiredArgument)
			}
			return &GetResult{
				Description: "Simulate manifest change",
				Messages: []Message{
					{
						Role: "user",
						Content: MessageContent{
							Type: "text",
							Text: fmt.Sprintf(strings.TrimSpace(`
I want to simulate the following change to my Meridian economy manifest before applying it:

%s

Please help me understand the impact of this change:

1. **Fetch current state**: Use meridian_economy_structure to understand the current manifest, then use meridian_sagas_list and meridian_instruments_list to get the full picture.

2. **Analyze the change**: Based on the current state, what exactly would this change modify, add, or remove?

3. **Identify breaking changes**: Would any existing sagas break due to this change? Check if any saga scripts reference instruments, account types, or handlers that would be affected.

4. **Assess downstream impact**: Which accounts, positions, or workflows would be affected by this change?

5. **Provide a recommendation**: Should I apply this change as-is, or do I need to make additional changes first? If there are risks, suggest a safer migration path.

6. **Generate updated manifest**: If the change is safe to apply, generate the updated manifest YAML that I can review before applying.
`), change),
						},
					},
				},
			}, nil
		},
	}
}

func buildDebugSaga() promptDef {
	return promptDef{
		meta: Prompt{
			Name:        "debug-saga",
			Description: "Diagnose a failed or stuck saga execution. Examines the execution log, identifies the failing step, checks compensation status, and suggests remediation.",
			Arguments: []Argument{
				{
					Name:        "saga_id",
					Description: "The saga execution ID (UUID) to debug.",
					Required:    true,
				},
			},
		},
		builder: func(args map[string]string) (*GetResult, error) {
			sagaID := args["saga_id"]
			if sagaID == "" {
				return nil, fmt.Errorf("%w: saga_id", ErrMissingRequiredArgument)
			}
			return &GetResult{
				Description: fmt.Sprintf("Debug saga execution %s", sagaID),
				Messages: []Message{
					{
						Role: "user",
						Content: MessageContent{
							Type: "text",
							Text: fmt.Sprintf(strings.TrimSpace(`
Please diagnose the failed or stuck saga execution %s.

Use the available tools to investigate:

1. **Examine the execution**: Use meridian_audit_saga_execution with saga_id=%s to retrieve the full execution log including all steps, their status, and any error messages.

2. **Identify the failure point**: Which step failed? What was the error message? Was it a validation failure, a service timeout, or a business logic error?

3. **Check compensation**: Did the saga trigger compensation? If so, did the compensation steps complete successfully? Are there any partially compensated states?

4. **Inspect the saga script**: Use meridian_saga_describe to retrieve the saga definition and examine the Starlark script. Does the failing step have a known issue?

5. **Check related state**: Use meridian_audit_position_movements and meridian_audit_journal_entries to see what state changes were made before the failure.

6. **Suggest remediation**:
   - If the saga is stuck (not compensated), what manual intervention is needed?
   - If there is a bug in the saga script, what fix is needed?
   - Is this a transient error that might succeed on retry?

Saga execution ID: %s
`), sagaID, sagaID, sagaID),
						},
					},
				},
			}, nil
		},
	}
}
