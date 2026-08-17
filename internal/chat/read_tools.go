// The read-tool decision loop: how one model step becomes reads, a decision, or
// a continuation, and how a rejected native tool schema is recognized.

package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/models"
)

const (
	maxReadToolRounds = 4
	maxAutoContinues  = 1
	readToolBatchSize = 4
	maxReadToolLimit  = 24
	maxToolContent    = 6_000
	readToolTimeout   = 10 * time.Second

	// continuationReadFactLimit bounds one read result carried into a length-stop
	// continuation. The continuation must keep the facts, not the payload.
	continuationReadFactLimit = 1_200
	// athenaStateBlockPrefix marks every application-owned system block —
	// contracts, task state, verified observations, retained state. They are
	// Athena's own records, so they survive any compaction the engine performs.
	athenaStateBlockPrefix = "[ATHENA"
)

type modelLoopResult struct {
	Content      string
	Messages     []models.Message
	ReadCalls    int
	DecisionTool string
}

func (s *Session) runReadToolLoop(ctx context.Context, messages []models.Message, status func(string)) (string, error) {
	result, err := s.runReadToolLoopState(ctx, messages, status)
	return result.Content, err
}

func (s *Session) runReadToolLoopState(ctx context.Context, messages []models.Message, status func(string)) (modelLoopResult, error) {
	return s.runReadToolLoopStateWithPolicy(ctx, messages, status, nil, false)
}

func (s *Session) runReadToolLoopStateWithPolicy(ctx context.Context, messages []models.Message, status func(string), onTool toolStepFunc, requireToolDecision bool, actionTypes ...[]string) (modelLoopResult, error) {
	messages = append([]models.Message(nil), messages...)
	tools := readToolDefinitions(actionTypes...)
	// Every no-native-tools request goes out provider-neutral. A model that
	// cannot render tool definitions usually cannot render tool history either,
	// so a later step of the same run must not replay the earlier steps' native
	// tool calls at it (R-02).
	if s.loop.ai.Name() == "Ollama" && s.nativeToolsDisabledModel == s.loop.ai.ChatModel() {
		if status != nil {
			status("Generating a plan")
		}
		return s.runPlainChatState(ctx, providerNeutralReadMessages(messages))
	}
	if supportProvider, ok := s.loop.ai.(ai.NativeToolSupportProvider); ok {
		support, err := supportProvider.NativeToolSupport(ctx)
		if err != nil || !support.Available {
			// Only a definitive capability answer is remembered: a failed probe is
			// a transport hiccup, and disabling native tools for the rest of the
			// session on one is a downgrade the user never asked for.
			if err == nil {
				s.nativeToolsDisabledModel = s.loop.ai.ChatModel()
			}
			if status != nil {
				status("Generating a plan")
			}
			return s.runPlainChatState(ctx, providerNeutralReadMessages(messages))
		}
	}
	continuations := 0
	readCalls := 0
	seenReads := make(map[string]bool)
	var partialResponses []string
	for round := 0; round < maxReadToolRounds; round++ {
		response, err := s.chatWithRetry(ctx, messages, tools, status, requireToolDecision)
		if err != nil {
			// Some local models can emit Athena action blocks but reject Ollama's
			// native tools schema or a follow-up request containing tool history.
			// Keep the turn useful without bypassing dispatch.
			if s.loop.ai.Name() == "Ollama" && isNativeToolRejection(err) {
				s.nativeToolsDisabledModel = s.loop.ai.ChatModel()
				if ctx.Err() != nil {
					return modelLoopResult{}, fmt.Errorf("Ollama native tools timed out before fallback; retrying the model without tools next turn")
				}
				if status != nil {
					status("Generating a plan")
				}
				fallbackMessages := providerNeutralReadMessages(messages)
				fallback, fallbackErr := s.runPlainChatResult(ctx, fallbackMessages)
				if fallbackErr == nil && strings.TrimSpace(fallback.Message.Content) != "" {
					fallbackMessages = append(fallbackMessages, fallback.Message)
					return modelLoopResult{Content: strings.TrimSpace(fallback.Message.Content), Messages: fallbackMessages, ReadCalls: readCalls}, nil
				}
				if fallbackErr == nil {
					fallbackErr = fmt.Errorf("model returned no visible response")
				}
				return modelLoopResult{}, fmt.Errorf("Ollama native tools were rejected and fallback chat failed: %w", fallbackErr)
			}
			return modelLoopResult{}, err
		}
		if len(response.Message.ToolCalls) == 0 {
			if strings.TrimSpace(response.Message.Content) == "" {
				return modelLoopResult{}, fmt.Errorf("model returned neither an answer nor a tool call")
			}
			if isIncompleteDoneReason(response.DoneReason) && continuations < maxAutoContinues {
				continuations++
				partialResponses = append(partialResponses, response.Message.Content)
				if status != nil {
					status("Compacting context and continuing the model response")
				}
				messages = compactContinuationMessages(messages, response.Message)
				messages = append(messages, models.Message{Role: "user", Content: "Continue the unfinished response from the saved state. Do not repeat completed work or emit duplicate actions."})
				continue
			}
			partialResponses = append(partialResponses, response.Message.Content)
			content := strings.TrimSpace(strings.Join(partialResponses, "\n\n"))
			response.Message.Content = content
			messages = append(messages, response.Message)
			return modelLoopResult{Content: content, Messages: messages, ReadCalls: readCalls}, nil
		}
		if len(response.Message.ToolCalls) > maxReadToolLimit {
			return modelLoopResult{}, fmt.Errorf("model requested %d read tools at once; safety limit is %d", len(response.Message.ToolCalls), maxReadToolLimit)
		}
		decision, decisionTool, hasDecision := decisionToolContentWithType(response.Message)
		if hasDecision && onlyDecisionTools(response.Message.ToolCalls) {
			// Do not retain an unresolved native tool call in provider history.
			// Convert it to Athena's provider-neutral decision representation; the
			// agent runner will either validate actions or return the question.
			decisionMessage := response.Message
			decisionMessage.ToolCalls = nil
			decisionMessage.Content = decision
			messages = append(messages, decisionMessage)
			return modelLoopResult{Content: decision, Messages: messages, ReadCalls: readCalls, DecisionTool: decisionTool}, nil
		}

		messages = append(messages, response.Message)
		calls := response.Message.ToolCalls
		for start := 0; start < len(calls); start += readToolBatchSize {
			end := min(start+readToolBatchSize, len(calls))
			if status != nil {
				status(fmt.Sprintf("Reading vault tools %d-%d of %d", start+1, end, len(calls)))
			}
			// The queue is application-owned: every accepted call is completed
			// before Athena asks the model to plan another turn.
			for _, call := range calls[start:end] {
				if call.Function.Name == "propose_actions" || call.Function.Name == "request_clarification" || call.Function.Name == "finish_run" {
					messages = append(messages, models.Message{
						Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID,
						Content: "Proposal not accepted in this read step. Use the read results, then return one updated valid actions array.",
					})
					continue
				}
				if readCalls >= maxReadToolLimit {
					messages = append(messages, models.Message{
						Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID,
						Content: fmt.Sprintf("Read budget exhausted after %d calls. Use the facts already returned.", maxReadToolLimit),
					})
					continue
				}
				signature := readCallSignature(call)
				if seenReads[signature] {
					messages = append(messages, models.Message{
						Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID,
						Content: "Duplicate read skipped. The result of this exact call is already present above; use it instead of requesting it again.",
					})
					continue
				}
				seenReads[signature] = true
				readCalls++
				if status != nil {
					status(readToolActivity(call))
				}
				toolName, target := strings.TrimSpace(call.Function.Name), readToolTarget(call)
				onTool.report(toolName, target, "started")
				content := s.executeReadTool(ctx, call)
				if readToolFailed(content) {
					onTool.report(toolName, target, "failed")
				} else {
					onTool.report(toolName, target, "succeeded")
				}
				messages = append(messages, models.Message{Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID, Content: content})
			}
		}
		if readCalls >= maxReadToolLimit {
			break
		}
	}
	// A weak model can keep requesting the same read tools forever. Do not make
	// the user retry from scratch: stop granting tools and ask for an answer
	// based on the facts Athena already supplied.
	if status != nil {
		status("Read limit reached — asking the model to finish with collected results")
	}
	messages = append(messages, models.Message{Role: "user", Content: "You have enough vault results. Answer the user now using the tool results already provided. Do not request more tools."})
	final, err := s.loop.ai.ChatWithToolsResult(ctx, messages, nil)
	if err != nil {
		return modelLoopResult{}, fmt.Errorf("finish after read limit: %w", err)
	}
	if strings.TrimSpace(final.Message.Content) == "" {
		return modelLoopResult{}, fmt.Errorf("model exceeded the %d-round read-tool limit without a final answer", maxReadToolRounds)
	}
	messages = append(messages, final.Message)
	return modelLoopResult{Content: strings.TrimSpace(final.Message.Content), Messages: messages, ReadCalls: readCalls}, nil
}

func decisionToolContent(message models.Message) (string, bool) {
	content, _, ok := decisionToolContentWithType(message)
	return content, ok
}

func decisionToolContentWithType(message models.Message) (string, string, bool) {
	var actions []ai.Action
	var question string
	var finalMessage string
	for _, call := range message.ToolCalls {
		switch call.Function.Name {
		case "propose_actions":
			_, proposed := ai.ExtractActions(string(call.Function.Arguments))
			actions = append(actions, proposed...)
		case "request_clarification":
			var args struct {
				Question string `json:"question"`
			}
			if json.Unmarshal(call.Function.Arguments, &args) == nil {
				question = strings.TrimSpace(args.Question)
			}
		case "finish_run":
			var args struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(call.Function.Arguments, &args) == nil {
				finalMessage = strings.TrimSpace(args.Message)
			}
		}
	}
	if len(actions) > 0 {
		raw, err := json.Marshal(actions)
		if err != nil {
			return "", "", false
		}
		content := strings.TrimSpace(message.Content)
		if content != "" {
			content += "\n\n"
		}
		content += "```action\n" + string(raw) + "\n```"
		return content, "propose_actions", true
	}
	if question != "" {
		return question, "request_clarification", true
	}
	if finalMessage != "" {
		return finalMessage, "finish_run", true
	}
	return "", "", false
}

func onlyDecisionTools(calls []models.ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		if call.Function.Name != "propose_actions" && call.Function.Name != "request_clarification" && call.Function.Name != "finish_run" {
			return false
		}
	}
	return true
}

func readCallSignature(call models.ToolCall) string {
	arguments := strings.TrimSpace(string(call.Function.Arguments))
	var compact any
	if json.Unmarshal(call.Function.Arguments, &compact) == nil {
		if raw, err := json.Marshal(compact); err == nil {
			arguments = string(raw)
		}
	}
	return strings.TrimSpace(call.Function.Name) + ":" + arguments
}

func (s *Session) chatWithRetry(ctx context.Context, messages []models.Message, tools []models.ToolDefinition, status func(string), requireTools bool) (ai.ToolChatResult, error) {
	if status != nil {
		status(fmt.Sprintf("%s · %s is generating a plan", s.loop.ai.Name(), shortModel(s.loop.ai.ChatModel())))
	}
	call := func() (ai.ToolChatResult, error) {
		if requireTools {
			if provider, ok := s.loop.ai.(ai.RequiredToolProvider); ok {
				return provider.ChatWithRequiredToolsResult(ctx, messages, tools)
			}
		}
		return s.loop.ai.ChatWithToolsResult(ctx, messages, tools)
	}
	response, err := call()
	if err == nil || !retryableModelError(err) || ctx.Err() != nil {
		return response, err
	}
	// A tool-schema failure from Ollama is normally model capability mismatch,
	// not a transient transport error. Return it immediately so runReadToolLoop
	// can fall back to ordinary chat while most of the turn budget remains.
	if s.loop.ai.Name() == "Ollama" && len(tools) > 0 {
		return response, err
	}
	if status != nil {
		status("Retrying the model request (1/1)")
	}
	select {
	case <-ctx.Done():
		return ai.ToolChatResult{}, ctx.Err()
	case <-time.After(250 * time.Millisecond):
	}
	return call()
}

func retryableModelError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "call ollama tools") || strings.Contains(text, "status 500") || strings.Contains(text, "status 502") || strings.Contains(text, "status 503") || strings.Contains(text, "status 504")
}

func isNativeToolRejection(err error) bool {
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "context deadline") || strings.Contains(text, "deadline exceeded") {
		return false
	}
	if !strings.Contains(text, "tool") {
		return false
	}
	return strings.Contains(text, "unsupported") ||
		strings.Contains(text, "not support") ||
		strings.Contains(text, "unknown field") ||
		strings.Contains(text, "function calling") ||
		strings.Contains(text, "status 400") ||
		strings.Contains(text, "status 422")
}

func isIncompleteDoneReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(reason, "length") || strings.Contains(reason, "context")
}

// compactContinuationMessages rebuilds the prompt for the one allowed
// continuation after a length stop. Conversation prose is dropped, but the
// application-owned blocks are not: the read results this turn already paid
// for, the pending-task JSON, and the task action contract, which since R-06
// is the model's only action vocabulary. Rebuilding from the goal alone left a
// continuation that could neither cite a vault fact nor name an action (R-05).
func compactContinuationMessages(messages []models.Message, partial models.Message) []models.Message {
	carried := make([]models.Message, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.Role == "tool":
			// ponytail: carry the read as a fact, not the full payload — the
			// continuation exists because the model already ran out of room. Raise
			// the limit only if a continuation is seen quoting truncated results.
			message.Content = compactText(message.Content, continuationReadFactLimit)
			carried = append(carried, message)
		case message.Role == "system" && strings.HasPrefix(message.Content, athenaStateBlockPrefix):
			carried = append(carried, message)
		}
	}
	system := models.Message{Role: "system", Content: ai.SystemPromptAt(time.Now())}
	goal := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			goal = messages[i].Content
			if marker := strings.Index(goal, "\n\n[ATHENA VAULT CONTEXT"); marker >= 0 {
				goal = goal[:marker]
			}
			goal = strings.TrimPrefix(goal, "User request:\n")
			break
		}
	}
	state := "Original user goal:\n" + truncateToolContent(goal) + "\n\nPartial response:\n" + truncateToolContent(partial.Content)
	// The assistant messages that requested those reads are gone, so the results
	// must travel as reference data rather than as orphan tool-protocol turns.
	compacted := append([]models.Message{system}, providerNeutralReadMessages(carried)...)
	return append(compacted, models.Message{Role: "system", Content: state})
}
