// Package pipeline implements the multi-agent execution model for the
// orchestrator. A pipeline is an ordered sequence of agent roles that
// execute for a work item, with support for iteration loops (e.g.
// producer-validator) and structured handoff between steps.
package pipeline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/drellabot/orchestrator/internal/config"
	"github.com/drellabot/orchestrator/internal/prompts"
)

// Verdict represents the validator's assessment of the producer's work.
type Verdict string

const (
	VerdictPass Verdict = "pass"
	VerdictFail Verdict = "fail"
)

// StepState tracks the execution state of one pipeline step.
type StepState struct {
	Role       string  `json:"role"`
	Status     string  `json:"status"`     // "pending", "running", "completed"
	Iterations int     `json:"iterations"` // how many times this step has run
	Verdict    Verdict `json:"verdict,omitempty"`
}

// State tracks the overall pipeline execution progress.
type State struct {
	Pipeline    string      `json:"pipeline"`
	CurrentStep int         `json:"current_step"`
	Iteration   int         `json:"iteration"`
	Steps       []StepState `json:"steps"`
}

// NewState creates a new pipeline state from a pipeline definition.
func NewState(pipelineName string, steps []config.PipelineStep) *State {
	ss := make([]StepState, len(steps))
	for i, step := range steps {
		ss[i] = StepState{
			Role:   step.Role,
			Status: "pending",
		}
	}
	return &State{
		Pipeline:    pipelineName,
		CurrentStep: 0,
		Iteration:   1,
		Steps:       ss,
	}
}

// IsMultiStep returns true if the pipeline has more than one step.
func IsMultiStep(steps []config.PipelineStep) bool {
	return len(steps) > 1
}

// TranscriptName returns the transcript filename for a given role and
// iteration. For single-step pipelines, it returns "transcript.jsonl"
// for backward compatibility.
func TranscriptName(role string, iteration int, multiStep bool) string {
	if !multiStep {
		return "transcript.jsonl"
	}
	return fmt.Sprintf("transcript-%s-%d.jsonl", role, iteration)
}

// BuildAgentSystemPrompt assembles the system prompt for an agent by
// combining the base prompt with the role-specific prompt loaded from
// the agents directory.
func BuildAgentSystemPrompt(agentsDir, role string) (string, error) {
	rolePrompt, err := prompts.LoadAgentPrompt(agentsDir, role)
	if err != nil {
		return "", err
	}
	return prompts.BuildSystemPrompt(prompts.Base, rolePrompt), nil
}

// BuildHandoffPrompt constructs the user prompt for a non-first agent step.
// It includes the original work item and context from the prior step based
// on the handoff configuration.
func BuildHandoffPrompt(workItem, diff, priorTranscript string, handoff config.HandoffConfig) string {
	var sb strings.Builder

	sb.WriteString(workItem)

	if handoff.IncludeDiffOrDefault() && diff != "" {
		sb.WriteString("\n\n## Changes from prior agent\n\n")
		sb.WriteString("```diff\n")
		sb.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n")
	}

	if handoff.IncludePriorTranscriptOrDefault() && priorTranscript != "" {
		sb.WriteString("\n\n## Prior agent's final message\n\n")
		sb.WriteString(priorTranscript)
		sb.WriteString("\n")
	}

	return sb.String()
}

// BuildFeedbackPrompt constructs the user prompt for a producer iteration
// that follows a failed validator verdict. It includes the original work
// item and the validator's findings.
func BuildFeedbackPrompt(workItem, findings string) string {
	var sb strings.Builder
	sb.WriteString(workItem)
	sb.WriteString("\n\n## Feedback from reviewer\n\n")
	sb.WriteString("The previous implementation was reviewed and the reviewer found issues that need to be addressed. ")
	sb.WriteString("The current state of the code is on the filesystem. Please fix the issues described below and ")
	sb.WriteString("use the `update_pr` tool to push your fixes, then `comment_on_pr` to summarize what you changed.\n\n")
	sb.WriteString(findings)
	sb.WriteString("\n")
	return sb.String()
}

// ParseVerdict scans a transcript (JSONL) for the validator's verdict.
// It looks for "VERDICT: pass" or "VERDICT: fail" in the last assistant
// message's text content. Returns the verdict and the full text of the
// last assistant message (which contains the findings).
func ParseVerdict(transcript []byte) (Verdict, string, error) {
	// Find the last assistant message with text content.
	var lastText string
	scanner := bufio.NewScanner(bytes.NewReader(transcript))
	for scanner.Scan() {
		line := scanner.Bytes()
		text := extractAssistantText(line)
		if text != "" {
			lastText = text
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("scanning transcript: %w", err)
	}

	if lastText == "" {
		return "", "", fmt.Errorf("no assistant text found in transcript")
	}

	// Look for VERDICT line.
	for _, line := range strings.Split(lastText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "VERDICT:") {
			verdict := strings.TrimSpace(strings.TrimPrefix(trimmed, "VERDICT:"))
			switch Verdict(verdict) {
			case VerdictPass:
				return VerdictPass, lastText, nil
			case VerdictFail:
				return VerdictFail, lastText, nil
			default:
				return "", lastText, fmt.Errorf("unknown verdict value: %q", verdict)
			}
		}
	}

	return "", lastText, fmt.Errorf("no VERDICT line found in assistant response")
}

// extractAssistantText extracts all text content from an assistant message
// in a stream-json transcript line.
func extractAssistantText(line []byte) string {
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &msg) != nil || msg.Type != "assistant" {
		return ""
	}

	var texts []string
	for _, c := range msg.Message.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// EscalationComment builds a PR comment summarizing iteration history
// when the max iteration cap is reached without a passing verdict.
func EscalationComment(iterations int, findings []string) string {
	var sb strings.Builder
	sb.WriteString("## Pipeline escalation: max iterations reached\n\n")
	sb.WriteString(fmt.Sprintf("The producer-validator loop ran %d iteration(s) without reaching a passing verdict. ", iterations))
	sb.WriteString("This PR needs human review to resolve the remaining issues.\n\n")

	for i, finding := range findings {
		sb.WriteString(fmt.Sprintf("### Iteration %d validator findings\n\n", i+1))
		// Truncate very long findings to keep the comment readable.
		if len(finding) > 4000 {
			finding = finding[:4000] + "\n\n... (truncated)"
		}
		sb.WriteString(finding)
		sb.WriteString("\n\n")
	}

	return sb.String()
}
