package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const aiResponseLimit = 4 << 20
const aiMessageLimit = 1536 << 10

type aiToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type aiProviderData struct {
	Format   string          `json:"format"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	BaseURL  string          `json:"baseURL"`
	Data     json.RawMessage `json:"data"`
}
type aiMessage struct {
	Role         string          `json:"role"`
	Content      string          `json:"content"`
	ToolCalls    []aiToolCall    `json:"toolCalls,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	ProviderData *aiProviderData `json:"providerData,omitempty"`
}
type aiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type aiChatRequest struct {
	RequestID string      `json:"requestId"`
	Provider  string      `json:"provider,omitempty"`
	Messages  []aiMessage `json:"messages"`
	Tools     []aiTool    `json:"tools"`
}
type aiChatResponse struct {
	Message      aiMessage `json:"message"`
	FinishReason string    `json:"finishReason,omitempty"`
}

var aiFunctionName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)

func aiJSONObject(raw []byte) bool {
	var obj map[string]json.RawMessage
	return json.Unmarshal(raw, &obj) == nil && obj != nil
}

func aiValidateChat(input aiChatRequest) error {
	if len(input.Messages) == 0 || len(input.Messages) > 160 || len(input.Tools) > 64 {
		return errors.New("AI 对话为空或过长，请新建对话")
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > aiMessageLimit {
		return errors.New("AI 对话内容过大，请新建对话或减少终端上下文")
	}
	names := map[string]bool{}
	for _, tool := range input.Tools {
		if !aiFunctionName.MatchString(tool.Name) || names[tool.Name] || !aiJSONObject(tool.Parameters) || len(tool.Description) > 8192 {
			return errors.New("AI 工具定义无效")
		}
		names[tool.Name] = true
	}
	pending, seen := map[string]bool{}, map[string]bool{}
	for i, m := range input.Messages {
		if m.Role != "assistant" && (len(m.ToolCalls) > 0 || m.ProviderData != nil) {
			return errors.New("AI 消息角色与工具调用不匹配")
		}
		if m.Role != "tool" && m.ToolCallID != "" {
			return errors.New("AI 工具结果角色无效")
		}
		switch m.Role {
		case "system":
			if i != 0 {
				return errors.New("AI 系统指令只能位于对话开头")
			}
		case "user", "assistant":
			if len(pending) > 0 {
				return errors.New("AI 工具调用尚未返回结果")
			}
		case "tool":
			if !pending[m.ToolCallID] {
				return errors.New("AI 工具结果与调用不匹配")
			}
			delete(pending, m.ToolCallID)
		default:
			return errors.New("AI 消息角色无效")
		}
		for _, call := range m.ToolCalls {
			if call.ID == "" || len(call.ID) > 256 || seen[call.ID] || !aiFunctionName.MatchString(call.Name) || !aiJSONObject([]byte(call.Arguments)) {
				return errors.New("AI 工具调用内容无效")
			}
			seen[call.ID], pending[call.ID] = true, true
		}
	}
	if len(pending) > 0 {
		return errors.New("AI 工具调用尚未返回结果")
	}
	return nil
}

func aiCheckProvider(p aiProvider, needModel bool) error {
	if err := aiNormalizeProvider(&p); err != nil {
		return err
	}
	if p.BaseURL == "" {
		return errors.New("请先填写 AI 接口地址")
	}
	if needModel && p.Model == "" {
		return errors.New("请先选择或填写支持工具调用的模型")
	}
	u, _ := url.Parse(p.BaseURL)
	ip := net.ParseIP(u.Hostname())
	local := strings.EqualFold(u.Hostname(), "localhost") || ip != nil && (ip.IsLoopback() || ip.IsPrivate())
	if p.APIKey == "" && !local {
		return errors.New("请先填写 AI 服务的 API Key")
	}
	return nil
}

func aiURL(p aiProvider, path string) string {
	base := strings.TrimRight(p.BaseURL, "/")
	switch p.Format {
	case "anthropic":
		base = strings.TrimSuffix(base, "/messages")
		if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
	case "gemini":
		if !strings.HasSuffix(base, "/v1beta") && !strings.HasSuffix(base, "/v1") && !strings.HasSuffix(base, "/v1alpha") {
			base += "/v1beta"
		}
	default:
		base = strings.TrimSuffix(base, "/chat/completions")
	}
	return base + path
}

func aiRequestJSON(ctx context.Context, client *http.Client, p aiProvider, method, endpoint string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errors.New("无法编码 AI 请求")
		}
		if len(data) > 2<<20 {
			return errors.New("AI 请求超过大小限制，请新建对话")
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return errors.New("AI 接口地址无效")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch p.Format {
	case "anthropic":
		req.Header.Set("anthropic-version", "2023-06-01")
		if p.APIKey != "" {
			req.Header.Set("x-api-key", p.APIKey)
		}
	case "gemini":
		if p.APIKey != "" {
			req.Header.Set("x-goog-api-key", p.APIKey)
		}
	default:
		if p.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return aiNetworkError(ctx, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, aiResponseLimit+1))
	if err != nil {
		return aiNetworkError(ctx, err)
	}
	if len(raw) > aiResponseLimit {
		return errors.New("AI 服务返回内容超过大小限制")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if p.Proxy.Type == "" || p.Proxy.Type == "system" {
			// Dynamic OS/environment proxies may echo credentials unknown to
			// this configuration, even in non-407 errors. Keep only the status.
			raw = nil
		}
		return aiAPIError(resp.StatusCode, raw, aiProviderSecrets(p)...)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("AI 服务返回了无效数据，请检查接口地址与协议设置")
	}
	return nil
}

func aiNetworkError(ctx context.Context, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return errors.New("AI 请求超时，可调大超时时间或换一个模型")
	case errors.Is(ctx.Err(), context.Canceled):
		return errors.New("AI 请求已取消")
	}
	// Do not surface URL errors: arbitrary gateways may include credentials in
	// redirects or transport error strings. Details belong to provider settings.
	return errors.New("AI 网络连接失败，请检查此配置的接口地址、代理和网络连接")
}

func aiAPIError(code int, raw []byte, secrets ...string) error {
	hint := "AI 服务拒绝了请求"
	switch {
	case code == 401:
		hint = "API Key 无效或已过期"
	case code == 402:
		hint = "AI 服务账户余额不足"
	case code == 403:
		hint = "没有 AI 服务访问权限"
	case code == 404:
		hint = "AI 接口地址或模型不存在"
	case code == 407:
		// A proxy error body can echo credentials, including system-proxy ones
		// outside this configuration. Never forward its arbitrary content.
		return errors.New("AI 代理认证失败（HTTP 407），请检查代理账号和密码")
	case code == 429:
		hint = "AI 请求过于频繁或额度已用完，请稍后重试"
	case code >= 500:
		hint = "AI 服务暂时不可用，请稍后重试"
	}
	var payload struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	msg := ""
	if json.Unmarshal(raw, &payload) == nil {
		var e struct {
			Message string `json:"message"`
		}
		var s string
		if json.Unmarshal(payload.Error, &e) == nil && e.Message != "" {
			msg = e.Message
		} else if json.Unmarshal(payload.Error, &s) == nil {
			msg = s
		} else {
			msg = payload.Message
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			for _, value := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
				msg = strings.ReplaceAll(msg, value, "[已隐藏密钥]")
			}
		}
	}
	if len([]rune(msg)) > 240 {
		msg = string([]rune(msg)[:240]) + "…"
	}
	if msg != "" {
		return fmt.Errorf("%s（HTTP %d）：%s", hint, code, msg)
	}
	return fmt.Errorf("%s（HTTP %d）", hint, code)
}

func aiNative(m aiMessage, p aiProvider) (map[string]json.RawMessage, error) {
	if m.ProviderData == nil {
		return nil, nil
	}
	d := m.ProviderData
	if d.Format != p.Format || d.Provider != p.ID || d.Model != p.Model || d.BaseURL != p.BaseURL {
		return nil, errors.New("AI 服务或模型已切换，请新建对话以避免混用工具调用记录")
	}
	var native map[string]json.RawMessage
	if json.Unmarshal(d.Data, &native) != nil || native == nil {
		return nil, errors.New("AI 对话的原始响应无效，请新建对话")
	}
	var role string
	_ = json.Unmarshal(native["role"], &role)
	want := "assistant"
	if p.Format == "gemini" {
		want = "model"
	}
	if role != want {
		return nil, errors.New("AI 原始响应角色无效")
	}
	if err := aiCheckNativeMessage(m, p, native); err != nil {
		return nil, err
	}
	return native, nil
}

// The opaque history and the normalized tool results must describe the same
// calls. In particular, pairing Gemini results by position without checking
// the original names/arguments could deliver a result to the wrong function.
func aiCheckNativeMessage(m aiMessage, p aiProvider, native map[string]json.RawMessage) error {
	var calls []aiToolCall
	content := ""
	invalid := errors.New("AI 对话原始记录与工具结果不一致，请新建对话")
	switch p.Format {
	case "openai":
		content = aiContentText(native["content"])
		if content == "" {
			_ = json.Unmarshal(native["refusal"], &content)
		}
		var raw []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if len(native["tool_calls"]) > 0 && json.Unmarshal(native["tool_calls"], &raw) != nil {
			return invalid
		}
		for _, call := range raw {
			if call.Type != "function" {
				return invalid
			}
			calls = append(calls, aiToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
	case "anthropic":
		var raw []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(native["content"], &raw) != nil {
			return invalid
		}
		for _, block := range raw {
			if block.Type == "text" {
				content += block.Text
			}
			if block.Type == "tool_use" {
				calls = append(calls, aiToolCall{ID: block.ID, Name: block.Name, Arguments: string(block.Input)})
			}
		}
	case "gemini":
		var raw []struct {
			Text         string `json:"text"`
			Thought      bool   `json:"thought"`
			FunctionCall *struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		}
		if json.Unmarshal(native["parts"], &raw) != nil {
			return invalid
		}
		for _, part := range raw {
			if !part.Thought {
				content += part.Text
			}
			if call := part.FunctionCall; call != nil {
				args := string(call.Args)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, aiToolCall{ID: call.ID, Name: call.Name, Arguments: args})
			}
		}
	}
	if content != m.Content || len(calls) != len(m.ToolCalls) {
		return invalid
	}
	for i, call := range calls {
		canonical := m.ToolCalls[i]
		if call.Name != canonical.Name || (call.ID != canonical.ID && !(p.Format == "gemini" && call.ID == "")) {
			return invalid
		}
		if !aiJSONObject([]byte(call.Arguments)) || !aiJSONObject([]byte(canonical.Arguments)) {
			return invalid
		}
		var left, right any
		l, r := json.NewDecoder(strings.NewReader(call.Arguments)), json.NewDecoder(strings.NewReader(canonical.Arguments))
		l.UseNumber()
		r.UseNumber()
		if l.Decode(&left) != nil || r.Decode(&right) != nil || !reflect.DeepEqual(left, right) {
			return invalid
		}
	}
	return nil
}

func aiPreserve(p aiProvider, raw json.RawMessage) *aiProviderData {
	return &aiProviderData{Format: p.Format, Provider: p.ID, Model: p.Model, BaseURL: p.BaseURL, Data: append(json.RawMessage(nil), raw...)}
}

func aiChat(ctx context.Context, client *http.Client, p aiProvider, input aiChatRequest) (aiChatResponse, error) {
	if err := aiCheckProvider(p, true); err != nil {
		return aiChatResponse{}, err
	}
	var result aiChatResponse
	var err error
	switch p.Format {
	case "anthropic":
		result, err = aiAnthropicChat(ctx, client, p, input)
	case "gemini":
		result, err = aiGeminiChat(ctx, client, p, input)
	default:
		result, err = aiOpenAIChat(ctx, client, p, input)
	}
	if err != nil {
		return aiChatResponse{}, err
	}
	if ctx.Err() != nil {
		return aiChatResponse{}, aiNetworkError(ctx, ctx.Err())
	}
	if result.Message.Role != "assistant" {
		return aiChatResponse{}, errors.New("AI 服务返回了无效的消息角色")
	}
	if len(result.Message.ToolCalls) > 32 {
		return aiChatResponse{}, errors.New("AI 服务一次返回过多操作，已停止")
	}
	if result.Message.Content == "" && len(result.Message.ToolCalls) == 0 {
		return aiChatResponse{}, errors.New("AI 模型未返回内容，请重试或换一个支持工具调用的模型")
	}
	if len(result.Message.ToolCalls) > 0 && (result.FinishReason == "length" || result.FinishReason == "max_tokens" || result.FinishReason == "MAX_TOKENS") {
		return aiChatResponse{}, errors.New("AI 工具调用被长度限制截断，已停止执行，请重试")
	}
	names, ids := map[string]bool{}, map[string]bool{}
	for _, t := range input.Tools {
		names[t.Name] = true
	}
	for _, call := range result.Message.ToolCalls {
		if call.ID == "" || len(call.ID) > 256 || ids[call.ID] || !names[call.Name] || !aiJSONObject([]byte(call.Arguments)) {
			return aiChatResponse{}, errors.New("AI 返回了无效或未提供的工具调用，已停止执行")
		}
		ids[call.ID] = true
	}
	return result, nil
}

func aiContentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func aiApplyEffort(body map[string]any, p aiProvider) {
	e := strings.ToLower(strings.TrimSpace(p.ReasoningEffort))
	if e == "" {
		return
	}
	u, _ := url.Parse(p.BaseURL)
	host := strings.ToLower(u.Hostname())
	hostIs := func(domains ...string) bool {
		for _, d := range domains {
			if host == d || strings.HasSuffix(host, "."+d) {
				return true
			}
		}
		return false
	}
	onOff := e == "none" || e == "off" || e == "disabled" || e == "on" || e == "enabled"
	on := e == "on" || e == "enabled"
	switch {
	case onOff && hostIs("bigmodel.cn", "z.ai", "deepseek.com"):
		mode := "disabled"
		if on {
			mode = "enabled"
		}
		body["thinking"] = map[string]string{"type": mode}
	case onOff && hostIs("dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com", "dashscope-us.aliyuncs.com", "maas.aliyuncs.com"):
		body["enable_thinking"] = on
	default:
		body["reasoning_effort"] = e
	}
}

func aiOpenAIChat(ctx context.Context, client *http.Client, p aiProvider, input aiChatRequest) (aiChatResponse, error) {
	messages := []any{}
	for _, m := range input.Messages {
		native, err := aiNative(m, p)
		if err != nil {
			return aiChatResponse{}, err
		}
		if native != nil {
			messages = append(messages, native)
			continue
		}
		message := map[string]any{"role": m.Role, "content": m.Content}
		if m.Role == "tool" {
			message["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			calls := []any{}
			for _, call := range m.ToolCalls {
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]string{"name": call.Name, "arguments": call.Arguments}})
			}
			message["tool_calls"] = calls
		}
		messages = append(messages, message)
	}
	body := map[string]any{"model": p.Model, "messages": messages, "stream": false}
	if len(input.Tools) > 0 {
		tools := []any{}
		for _, t := range input.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters}})
		}
		body["tools"] = tools
	}
	aiApplyEffort(body, p)
	var out struct {
		Choices []struct {
			Message      json.RawMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := aiRequestJSON(ctx, client, p, "POST", aiURL(p, "/chat/completions"), body, &out); err != nil {
		return aiChatResponse{}, err
	}
	if len(out.Choices) == 0 {
		return aiChatResponse{}, errors.New("AI 服务未返回消息")
	}
	c := out.Choices[0]
	if c.FinishReason == "content_filter" {
		return aiChatResponse{}, errors.New("AI 服务的内容策略阻止了此次响应")
	}
	var m struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Refusal   string          `json:"refusal"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal(c.Message, &m) != nil {
		return aiChatResponse{}, errors.New("AI 服务消息格式无效")
	}
	message := aiMessage{Role: m.Role, Content: aiContentText(m.Content), ProviderData: aiPreserve(p, c.Message)}
	if message.Content == "" {
		message.Content = m.Refusal
	}
	for _, call := range m.ToolCalls {
		if call.Type != "function" {
			return aiChatResponse{}, errors.New("AI 返回了不支持的工具类型")
		}
		message.ToolCalls = append(message.ToolCalls, aiToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	return aiChatResponse{Message: message, FinishReason: c.FinishReason}, nil
}

func aiAnthropicChat(ctx context.Context, client *http.Client, p aiProvider, input aiChatRequest) (aiChatResponse, error) {
	messages := []map[string]any{}
	system := ""
	for _, m := range input.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		native, err := aiNative(m, p)
		if err != nil {
			return aiChatResponse{}, err
		}
		if native != nil {
			// Full content preserves thinking/signature/redacted_thinking blocks;
			// response envelope fields (usage/model/id) are not message fields.
			messages = append(messages, map[string]any{"role": "assistant", "content": native["content"]})
			continue
		}
		role, blocks := m.Role, []any{}
		if m.Role == "tool" {
			role = "user"
			blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content})
		} else {
			if m.Content != "" {
				blocks = append(blocks, map[string]string{"type": "text", "text": m.Content})
			}
			for _, call := range m.ToolCalls {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": json.RawMessage(call.Arguments)})
			}
		}
		// Parallel results must appear together immediately after tool_use.
		if len(messages) > 0 && role == "user" && messages[len(messages)-1]["role"] == role {
			last := messages[len(messages)-1]
			if previous, ok := last["content"].([]any); ok {
				last["content"] = append(previous, blocks...)
				continue
			}
		}
		messages = append(messages, map[string]any{"role": role, "content": blocks})
	}
	body := map[string]any{"model": p.Model, "messages": messages, "max_tokens": 8192}
	if system != "" {
		body["system"] = system
	}
	if p.ReasoningEffort != "" {
		body["output_config"] = map[string]string{"effort": p.ReasoningEffort}
	}
	if len(input.Tools) > 0 {
		tools := []any{}
		for _, t := range input.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Parameters})
		}
		body["tools"] = tools
	}
	var raw json.RawMessage
	if err := aiRequestJSON(ctx, client, p, "POST", aiURL(p, "/messages"), body, &raw); err != nil {
		return aiChatResponse{}, err
	}
	var out struct {
		Role       string `json:"role"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return aiChatResponse{}, errors.New("AI 服务消息格式无效")
	}
	if out.StopReason == "refusal" {
		return aiChatResponse{}, errors.New("AI 服务拒绝了此次请求")
	}
	message := aiMessage{Role: out.Role, ProviderData: aiPreserve(p, raw)}
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			message.Content += block.Text
		case "tool_use":
			message.ToolCalls = append(message.ToolCalls, aiToolCall{ID: block.ID, Name: block.Name, Arguments: string(block.Input)})
		}
	}
	return aiChatResponse{Message: message, FinishReason: out.StopReason}, nil
}

func aiGeminiChat(ctx context.Context, client *http.Client, p aiProvider, input aiChatRequest) (aiChatResponse, error) {
	contents := []map[string]any{}
	system := ""
	type callInfo struct{ name, nativeID string }
	calls := map[string]callInfo{}
	for _, m := range input.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		for _, call := range m.ToolCalls {
			calls[call.ID] = callInfo{name: call.Name}
		}
		native, err := aiNative(m, p)
		if err != nil {
			return aiChatResponse{}, err
		}
		if native != nil {
			var parts []struct {
				FunctionCall *struct {
					ID string `json:"id"`
				} `json:"functionCall"`
			}
			_ = json.Unmarshal(native["parts"], &parts)
			i := 0
			for _, part := range parts {
				if part.FunctionCall != nil && i < len(m.ToolCalls) {
					call := m.ToolCalls[i]
					info := calls[call.ID]
					info.nativeID = part.FunctionCall.ID
					calls[call.ID] = info
					i++
				}
			}
			contents = append(contents, map[string]any{"role": "model", "parts": native["parts"]})
			continue
		}
		role, parts := m.Role, []any{}
		if role == "assistant" {
			role = "model"
		}
		if m.Role == "tool" {
			role = "user"
			info := calls[m.ToolCallID]
			if info.name == "" {
				return aiChatResponse{}, errors.New("AI 工具结果缺少对应调用")
			}
			result := map[string]any{"name": info.name, "response": map[string]string{"output": m.Content}}
			if info.nativeID != "" {
				result["id"] = info.nativeID
			}
			parts = append(parts, map[string]any{"functionResponse": result})
		} else {
			if m.Content != "" {
				parts = append(parts, map[string]string{"text": m.Content})
			}
			for _, call := range m.ToolCalls {
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.Name, "args": json.RawMessage(call.Arguments)}})
			}
		}
		if len(contents) > 0 && role == "user" && contents[len(contents)-1]["role"] == role {
			last := contents[len(contents)-1]
			if previous, ok := last["parts"].([]any); ok {
				last["parts"] = append(previous, parts...)
				continue
			}
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	body := map[string]any{"contents": contents}
	if system != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": system}}}
	}
	if effort := strings.ToLower(p.ReasoningEffort); effort != "" {
		thinking := map[string]any{}
		if effort == "none" || effort == "off" {
			thinking["thinkingBudget"] = 0
		} else if n, err := strconv.Atoi(effort); err == nil {
			thinking["thinkingBudget"] = n
		} else {
			thinking["thinkingLevel"] = effort
		}
		body["generationConfig"] = map[string]any{"thinkingConfig": thinking}
	}
	if len(input.Tools) > 0 {
		declarations := []any{}
		for _, t := range input.Tools {
			declarations = append(declarations, map[string]any{"name": t.Name, "description": t.Description, "parametersJsonSchema": t.Parameters})
		}
		body["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	var out struct {
		Candidates []struct {
			Content      json.RawMessage `json:"content"`
			FinishReason string          `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	endpoint := aiURL(p, "/models/"+url.PathEscape(strings.TrimPrefix(p.Model, "models/"))+":generateContent")
	if err := aiRequestJSON(ctx, client, p, "POST", endpoint, body, &out); err != nil {
		return aiChatResponse{}, err
	}
	if out.PromptFeedback.BlockReason != "" {
		return aiChatResponse{}, errors.New("Gemini 内容策略阻止了此次请求")
	}
	if len(out.Candidates) == 0 {
		return aiChatResponse{}, errors.New("AI 服务未返回消息")
	}
	c := out.Candidates[0]
	switch c.FinishReason {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION", "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL":
		return aiChatResponse{}, errors.New("Gemini 拒绝了响应或返回了无效工具调用，已停止执行")
	}
	var content struct {
		Role  string `json:"role"`
		Parts []struct {
			Text         string `json:"text"`
			Thought      bool   `json:"thought"`
			FunctionCall *struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		} `json:"parts"`
	}
	if json.Unmarshal(c.Content, &content) != nil || content.Role != "model" {
		return aiChatResponse{}, errors.New("AI 服务消息格式无效")
	}
	message := aiMessage{Role: "assistant", ProviderData: aiPreserve(p, c.Content)}
	for _, part := range content.Parts {
		if !part.Thought {
			message.Content += part.Text
		}
		if f := part.FunctionCall; f != nil {
			id := f.ID
			if id == "" {
				id = "gemini-" + randomID()
			}
			args := string(f.Args)
			if args == "" {
				args = "{}"
			}
			message.ToolCalls = append(message.ToolCalls, aiToolCall{ID: id, Name: f.Name, Arguments: args})
		}
	}
	return aiChatResponse{Message: message, FinishReason: c.FinishReason}, nil
}

func aiListModels(ctx context.Context, client *http.Client, p aiProvider) ([]string, error) {
	if err := aiCheckProvider(p, false); err != nil {
		return nil, err
	}
	ids, page := map[string]bool{}, ""
	for n := 0; n < 10; n++ {
		switch p.Format {
		case "anthropic":
			path := "/models?limit=1000"
			if page != "" {
				path += "&after_id=" + url.QueryEscape(page)
			}
			var out struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
				HasMore bool   `json:"has_more"`
				LastID  string `json:"last_id"`
			}
			if err := aiRequestJSON(ctx, client, p, "GET", aiURL(p, path), nil, &out); err != nil {
				return nil, err
			}
			for _, m := range out.Data {
				ids[m.ID] = true
			}
			if !out.HasMore {
				page = ""
			} else if out.LastID == "" || out.LastID == page {
				return nil, errors.New("AI 模型列表分页无效，请手动填写模型")
			} else {
				page = out.LastID
			}
		case "gemini":
			path := "/models?pageSize=1000"
			if page != "" {
				path += "&pageToken=" + url.QueryEscape(page)
			}
			var out struct {
				Models []struct {
					Name    string   `json:"name"`
					Methods []string `json:"supportedGenerationMethods"`
				} `json:"models"`
				Next string `json:"nextPageToken"`
			}
			if err := aiRequestJSON(ctx, client, p, "GET", aiURL(p, path), nil, &out); err != nil {
				return nil, err
			}
			for _, m := range out.Models {
				for _, method := range m.Methods {
					if method == "generateContent" {
						ids[strings.TrimPrefix(m.Name, "models/")] = true
						break
					}
				}
			}
			if page != "" && page == out.Next {
				return nil, errors.New("AI 模型列表分页无效，请手动填写模型")
			}
			page = out.Next
		default:
			var out struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := aiRequestJSON(ctx, client, p, "GET", aiURL(p, "/models"), nil, &out); err != nil {
				return nil, err
			}
			for _, m := range out.Data {
				ids[m.ID] = true
			}
		}
		if page == "" {
			break
		}
		if n == 9 {
			return nil, errors.New("AI 模型列表分页过多，请手动填写模型")
		}
	}
	delete(ids, "")
	result := make([]string, 0, len(ids))
	for id := range ids {
		if len(id) <= 1024 {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, errors.New("AI 服务没有返回可用模型，请手动填写模型名")
	}
	return result, nil
}
