package plg_widget_ai

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const systemPrompt = `You are the file assistant built into Filestash, a web file manager connected to the user's storages (computers, drives, clouds, phones, email).
You help the user find, understand, analyse and organise their files by calling tools. Rules:
- Use tools to look before answering, never invent file names or content.
- Paths are absolute from the root of the current storage or relative to the current directory. Directories end with "/".
- Move and rename are done right away, so only do it when asked or when it is the obvious next step of the request. Double check the destination.
- Deletions always go through the delete tool which asks the user for confirmation.
- When you learn something durable about the user (how they name things, where things belong, their projects, their preferences) save it with the remember tool. Don't save transient things.
- Be concise. When you did something, list what changed.

Current date: %s
Current directory: %s
%s`

type Request struct {
	Message string    `json:"message"`
	History []Message `json:"history"`
	Path    string    `json:"path"`
}

type Response struct {
	Reply   string   `json:"reply"`
	Actions []Action `json:"actions"`
	Pending []Action `json:"pending"`
	Steps   []string `json:"steps"`
}

func buildContext(user string) string {
	var b strings.Builder
	if mems := memories(user); len(mems) > 0 {
		b.WriteString("\nWhat you remember about the user:\n")
		for _, m := range mems {
			fmt.Fprintf(&b, "- #%d %s\n", m.ID, m.Content)
		}
	}
	if j := recentJournal(user, 20); len(j) > 0 {
		b.WriteString("\nRecent actions you performed (use them to follow the user's organisation habits):\n")
		for _, a := range j {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	return b.String()
}

func runAgent(ctx context.Context, llm LLM, sess *Session, req Request, maxSteps int) (Response, error) {
	messages := []Message{{
		Role:    "system",
		Content: fmt.Sprintf(systemPrompt, time.Now().Format("2006-01-02"), sess.cwd, buildContext(sess.user)),
	}}
	history := req.History
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	for _, m := range history {
		if (m.Role == "user" || m.Role == "assistant") && m.Content != "" {
			messages = append(messages, Message{Role: m.Role, Content: truncate(m.Content, 4000)})
		}
	}
	messages = append(messages, Message{Role: "user", Content: req.Message})

	res := Response{Actions: []Action{}, Pending: []Action{}, Steps: []string{}}
	if maxSteps <= 0 {
		maxSteps = 12
	}
	for step := 0; step <= maxSteps; step++ {
		tools := toolDefs
		if step == maxSteps {
			tools = nil // force a final answer
		}
		msg, err := llm.Chat(ctx, messages, tools)
		if err != nil {
			return res, err
		}
		if len(msg.ToolCalls) == 0 {
			res.Reply = msg.Content
			break
		}
		messages = append(messages, msg)
		for _, tc := range msg.ToolCalls {
			res.Steps = append(res.Steps, fmt.Sprintf("%s(%s)", tc.Function.Name, truncate(tc.Function.Arguments, 200)))
			out := sess.call(tc.Function.Name, tc.Function.Arguments)
			messages = append(messages, Message{Role: "tool", ToolCallID: tc.ID, Content: truncate(out, 12000)})
		}
	}
	res.Actions, res.Pending = sess.Actions, sess.Pending
	if res.Reply == "" {
		res.Reply = "Done."
	}
	return res, nil
}
