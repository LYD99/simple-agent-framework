package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"simple-agent-framework/model"
)

const DefaultToolResultMaxLen = 30000
const DefaultRecentToolResultTokens = 40000

const DefaultCompressPrompt = `You are a conversation context compression assistant. Your task is to compress the conversation history below into a structured summary while preserving all critical information.

Requirements:
1. Preserve the user's core goals and specific instructions exactly as stated
2. Retain all important findings, conclusions, and error messages
3. Record completed steps with their outcomes (success/failure/partial)
4. Keep all key file paths, code references, URLs, and identifiers
5. Do not omit any unfinished tasks, pending decisions, or open questions
6. Use concise language — eliminate redundancy but never lose meaning

Output the summary as a JSON object with these fields: goals, instructions, findings, progress, reference_files`

type ConversationSummary struct {
	Goals          string `json:"goals"`
	Instructions   string `json:"instructions"`
	Findings       string `json:"findings"`
	Progress       string `json:"progress"`
	ReferenceFiles string `json:"reference_files"`
}

type ContextCompressor struct {
	model           model.ChatModel
	prompt          string
	maxContextRatio float64
}

func NewContextCompressor(mainModel model.ChatModel, compressModel model.ChatModel, prompt string, maxContextRatio float64) *ContextCompressor {
	cm := compressModel
	if cm == nil {
		cm = mainModel
	}
	p := prompt
	if p == "" {
		p = DefaultCompressPrompt
	}
	mcr := maxContextRatio
	if mcr <= 0 || mcr > 1 {
		mcr = 0.9
	}
	return &ContextCompressor{
		model:           cm,
		prompt:          p,
		maxContextRatio: mcr,
	}
}

func (c *ContextCompressor) ShouldCompress(messages []model.ChatMessage, modelMaxTokens int) bool {
	if modelMaxTokens <= 0 {
		return false
	}
	total := estimateMessagesTokens(messages)
	return float64(total)/float64(modelMaxTokens) > c.maxContextRatio
}

func (c *ContextCompressor) Compress(ctx context.Context, messages []model.ChatMessage) ([]model.ChatMessage, error) {
	serialized := serializeMessages(messages)
	msgs := []model.ChatMessage{
		{Role: model.RoleUser, Content: c.prompt + "\n\n---\n\n" + serialized},
	}
	resp, err := c.model.Generate(ctx, msgs)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(resp.Message.Content)
	var summary ConversationSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		summary = ConversationSummary{Goals: raw}
	}
	summaryText := renderSummary(&summary)

	leadingSys := 0
	for leadingSys < len(messages) && messages[leadingSys].Role == model.RoleSystem {
		leadingSys++
	}
	lastAsst := -1
	for i := range messages {
		if messages[i].Role == model.RoleAssistant {
			lastAsst = i
		}
	}
	var recent []model.ChatMessage
	if lastAsst >= 0 {
		recent = append(recent, messages[lastAsst:]...)
	}

	out := make([]model.ChatMessage, 0, leadingSys+1+len(recent))
	out = append(out, messages[:leadingSys]...)
	out = append(out, model.ChatMessage{
		Role:    model.RoleUser,
		Content: summaryText,
	})
	out = append(out, recent...)
	return out, nil
}

func estimateTokens(s string) int {
	return len(s) / 4
}

func estimateMessagesTokens(messages []model.ChatMessage) int {
	n := 0
	for i := range messages {
		n += estimateTokens(messages[i].Content)
	}
	return n
}

func isToolFailureContent(content string) bool {
	t := strings.TrimSpace(content)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "error") {
		return true
	}
	return strings.Contains(lower, "error:")
}

func isAssistantWithToolCalls(m model.ChatMessage) bool {
	return m.Role == model.RoleAssistant && len(m.ToolCalls) > 0
}

func PruneConsecutiveFailures(messages []model.ChatMessage) []model.ChatMessage {
	if len(messages) < 2 {
		return append([]model.ChatMessage(nil), messages...)
	}
	out := make([]model.ChatMessage, 0, len(messages))
	i := 0
	for i < len(messages) {
		if i+1 < len(messages) &&
			isAssistantWithToolCalls(messages[i]) &&
			messages[i+1].Role == model.RoleTool &&
			isToolFailureContent(messages[i+1].Content) {
			toolName := messages[i+1].Name
			run := [][2]int{{i, i + 1}}
			j := i + 2
			for j+1 < len(messages) &&
				isAssistantWithToolCalls(messages[j]) &&
				messages[j+1].Role == model.RoleTool &&
				isToolFailureContent(messages[j+1].Content) &&
				messages[j+1].Name == toolName {
				run = append(run, [2]int{j, j + 1})
				j += 2
			}
			last := run[len(run)-1]
			out = append(out, messages[last[0]], messages[last[1]])
			i = j
			continue
		}
		out = append(out, messages[i])
		i++
	}
	return out
}

func TruncateToolResult(ctx context.Context, content string, maxLen int, store ContentStore, callID string) string {
	if len(content) <= maxLen {
		return content
	}
	if store != nil {
		_ = store.Store(ctx, callID, content)
	}
	X := len(content)
	ins := fmt.Sprintf(
		"\n\n... [content truncated] ...\n[Original length: %d chars, truncated to %d chars. Full content persisted with ID: %s. Use tool 'fetch_full_result' with this ID to retrieve the complete content.]\n\n",
		X, maxLen, callID,
	)
	budget := maxLen - len(ins)
	if budget < 2 {
		headLen := maxLen * 80 / 100
		tailLen := maxLen - headLen
		if headLen+tailLen > len(content) {
			return content
		}
		return content[:headLen] + content[len(content)-tailLen:]
	}
	headLen := budget * 80 / 100
	tailLen := budget - headLen
	if headLen+tailLen > len(content) {
		return content
	}
	return content[:headLen] + ins + content[len(content)-tailLen:]
}

func CompactStaleToolResults(messages []model.ChatMessage, tokenBudget int) []model.ChatMessage {
	const cleared = "[Old tool result content cleared]"
	out := make([]model.ChatMessage, len(messages))
	copy(out, messages)
	running := 0
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != model.RoleTool {
			continue
		}
		if out[i].Content == cleared {
			continue
		}
		tok := estimateTokens(out[i].Content)
		if running+tok <= tokenBudget {
			running += tok
			continue
		}
		for j := i; j >= 0; j-- {
			if out[j].Role == model.RoleTool && out[j].Content != cleared {
				out[j] = model.ChatMessage{
					Role:       model.RoleTool,
					Content:    cleared,
					ToolCallID: out[j].ToolCallID,
					Name:       out[j].Name,
				}
			}
		}
		break
	}
	return out
}

func serializeMessages(messages []model.ChatMessage) string {
	var b strings.Builder
	for i := range messages {
		b.WriteString("[")
		b.WriteString(string(messages[i].Role))
		b.WriteString("]: ")
		b.WriteString(messages[i].Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func renderSummary(s *ConversationSummary) string {
	var b strings.Builder
	b.WriteString("## Goals\n\n")
	b.WriteString(s.Goals)
	b.WriteString("\n\n## Instructions\n\n")
	b.WriteString(s.Instructions)
	b.WriteString("\n\n## Findings\n\n")
	b.WriteString(s.Findings)
	b.WriteString("\n\n## Progress\n\n")
	b.WriteString(s.Progress)
	b.WriteString("\n\n## Reference files\n\n")
	b.WriteString(s.ReferenceFiles)
	return b.String()
}
