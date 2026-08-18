// vision_remote.go - converts image content parts into text via a configurable
// remote vision model (OpenAI / Anthropic compatible), with local ds-vision
// fallback.
//
// When a channel has VisionEnabled=true and VisionMode=remote, pasted/dragged
// images are:
//   1) persisted to D:\ds-vision-data\pasted\dsv_<hash>.<ext> (shared with the
//      local ds-vision path)
//   2) sent to the configured remote vision model (provider/baseURL/apiKey can
//      either follow the main model adapter or be overridden)
//   3) replaced in the outgoing message with the recognized text summary plus
//      the real file paths, so a text-only model (DeepSeek) can answer.
//
// If the remote call fails, the local ds-vision skill is used as a fallback and
// the failure reason is preserved in the summary text.

package modeladapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cursor/internal/modelchannel"
	"cursor/internal/netproxy"
)

const (
	// remoteVisionTimeout 是远程视觉模型单次识别的超时时间。
	remoteVisionTimeout = 150 * time.Second
	// remoteVisionDefaultMaxTokens 是远程视觉模型识别的默认输出上限。
	remoteVisionDefaultMaxTokens = 2048
	// visionRemoteFailurePrefix 与本地识别的失败前缀保持一致。
	visionRemoteFailurePrefix = "[图片自动识别失败"
)

// VisionConfig 描述远程视觉模型的解析后配置。
type VisionConfig struct {
	// Mode 表示图片输入的处理方式：local（本机 ds-vision）或 remote（远程视觉模型）。
	Mode string
	// UseProviderDefaults 表示 provider/baseURL/apiKey 是否跟随主模型适配器。
	UseProviderDefaults bool
	// ProviderType 表示远程视觉模型的服务提供商类型：openai 或 anthropic。
	ProviderType string
	// ModelID 表示远程视觉模型的模型标识。
	ModelID string
	// BaseURL 表示远程视觉模型的 API 根地址。
	BaseURL string
	// APIKey 表示远程视觉模型的访问密钥。
	APIKey string
}

// RemoteEnabled 判断是否启用了远程视觉模型识别。
func (cfg VisionConfig) RemoteEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Mode), "remote") &&
		strings.TrimSpace(cfg.ModelID) != ""
}

// ConvertImageMessagesWithVision 返回消息副本：图片内容块按视觉配置转成文本摘要。
// 配置为 remote 且指定了视觉模型时使用远程视觉模型（失败回退本机 ds-vision），
// 否则保持原有的本机 ds-vision 行为。
func ConvertImageMessagesWithVision(ctx context.Context, messages []Message, cfg VisionConfig) []Message {
	if len(messages) == 0 {
		return messages
	}
	if !cfg.RemoteEnabled() {
		return ConvertImageMessages(ctx, messages)
	}

	skillDir := visionEnvOr("DS_VISION_SKILL_DIR", visionBridgeDefaultSkillDir)
	dataDir := visionEnvOr("DS_VISION_DATA_DIR", visionBridgeDefaultDataDir)
	pwsh := visionEnvOr("DS_VISION_POWERSHELL", visionBridgeDefaultPowerShell)
	orchestrator := filepath.Join(skillDir, "scripts", "ds-vision.ps1")
	pastedDir := filepath.Join(dataDir, "pasted")
	tmpDir := filepath.Join(dataDir, "tmp")

	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if !hasImageContentParts(msg.ContentParts) {
			out = append(out, msg)
			continue
		}
		out = append(out, convertImageMessageRemote(ctx, msg, cfg, orchestrator, pwsh, pastedDir, tmpDir))
	}
	return out
}

func convertImageMessageRemote(
	ctx context.Context,
	msg Message,
	cfg VisionConfig,
	orchestrator string,
	pwsh string,
	pastedDir string,
	tmpDir string,
) Message {
	persisted, ok := persistVisionImageRefs(msg, pastedDir)
	if !ok {
		msg.ContentParts = nil
		msg.Content = "[图片自动识别失败: 无法保存图片文件]"
		return msg
	}

	prompt := persisted.textParts
	if prompt == "" {
		prompt = "请描述这些图片的内容。"
	}
	cacheKey := visionCacheKey(persisted.refs, prompt)
	resultText := visionCacheGet(cacheKey)
	if resultText == "" {
		resultText = runRemoteVision(ctx, cfg, prompt, persisted.refs)
		if strings.HasPrefix(resultText, visionRemoteFailurePrefix) {
			// 远程视觉模型失败时回退到本机 ds-vision，并保留失败原因。
			localResult := runVisionOrchestrator(ctx, orchestrator, pwsh, tmpDir, persisted.refs, prompt)
			if !strings.HasPrefix(localResult, visionRemoteFailurePrefix) {
				reason := strings.TrimSpace(strings.TrimPrefix(resultText, visionRemoteFailurePrefix))
				reason = strings.TrimSuffix(reason, "]")
				resultText = fmt.Sprintf("（远程视觉模型识别失败：%s，已回退本机识别）\n\n%s", reason, localResult)
			}
		}
		if !strings.HasPrefix(resultText, visionRemoteFailurePrefix) {
			visionCacheSet(cacheKey, resultText)
		}
	}
	return buildVisionResultMessage(msg, persisted.textParts, persisted.pathLines, resultText)
}

func runRemoteVision(ctx context.Context, cfg VisionConfig, prompt string, refs []map[string]string) string {
	text, err := RunRemoteVisionErr(ctx, cfg, prompt, refs)
	if err != nil {
		return fmt.Sprintf("[图片自动识别失败: %v]", err)
	}
	return text
}

// RunRemoteVisionErr 调用远程视觉模型并返回 (文本, 错误)。供模型编辑器“测试视觉模型”使用。
func RunRemoteVisionErr(ctx context.Context, cfg VisionConfig, prompt string, refs []map[string]string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.ProviderType)) {
	case "anthropic":
		return remoteAnthropicVisionCall(ctx, cfg, prompt, refs)
	default:
		return remoteOpenAIVisionCall(ctx, cfg, prompt, refs)
	}
}

// remoteOpenAIVisionCall 调用 OpenAI 兼容 /chat/completions 接口识别图片。
func remoteOpenAIVisionCall(ctx context.Context, cfg VisionConfig, prompt string, refs []map[string]string) (string, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	modelID := strings.TrimSpace(cfg.ModelID)
	if apiKey == "" {
		return "", errors.New("远程视觉模型 APIKey 为空")
	}
	if baseURL == "" {
		return "", errors.New("远程视觉模型接口地址为空")
	}
	if modelID == "" {
		return "", errors.New("远程视觉模型标识为空")
	}

	content := make([]map[string]any, 0, len(refs)+1)
	content = append(content, map[string]any{"type": contentPartTypeText, "text": prompt})
	imageCount := 0
	for _, ref := range refs {
		path := strings.TrimSpace(ref["value"])
		payload, mime, err := readVisionRefFile(path)
		if err != nil {
			continue
		}
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(payload),
			},
		})
		imageCount++
	}
	if imageCount == 0 {
		return "", errors.New("没有可发送的图片文件")
	}

	body := map[string]any{
		"model":      modelID,
		"messages":   []map[string]any{{"role": "user", "content": content}},
		"max_tokens": remoteVisionDefaultMaxTokens,
	}
	requestURL := OpenAIEndpointURL(baseURL, modelchannel.OpenAIEndpointChatCompletions)

	var parsed struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := postVisionJSON(ctx, requestURL, apiKey, body, "openai", &parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return "", errors.New(strings.TrimSpace(parsed.Error.Message))
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("远程视觉模型没有返回内容")
	}
	return normalizeVisionTextContent(parsed.Choices[0].Message.Content)
}

// remoteAnthropicVisionCall 调用 Anthropic 兼容 /v1/messages 接口识别图片。
func remoteAnthropicVisionCall(ctx context.Context, cfg VisionConfig, prompt string, refs []map[string]string) (string, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	modelID := strings.TrimSpace(cfg.ModelID)
	if apiKey == "" {
		return "", errors.New("远程视觉模型 APIKey 为空")
	}
	if baseURL == "" {
		return "", errors.New("远程视觉模型接口地址为空")
	}
	if modelID == "" {
		return "", errors.New("远程视觉模型标识为空")
	}

	content := make([]map[string]any, 0, len(refs)+1)
	imageCount := 0
	for _, ref := range refs {
		path := strings.TrimSpace(ref["value"])
		payload, mime, err := readVisionRefFile(path)
		if err != nil {
			continue
		}
		content = append(content, map[string]any{
			"type": contentPartTypeImage,
			"source": map[string]any{
				"type":       "base64",
				"media_type": mime,
				"data":       base64.StdEncoding.EncodeToString(payload),
			},
		})
		imageCount++
	}
	if imageCount == 0 {
		return "", errors.New("没有可发送的图片文件")
	}
	content = append(content, map[string]any{"type": contentPartTypeText, "text": prompt})

	body := map[string]any{
		"model":      modelID,
		"max_tokens": remoteVisionDefaultMaxTokens,
		"messages":   []map[string]any{{"role": "user", "content": content}},
	}
	requestURL := anthropicEndpointURL(baseURL)

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := postVisionJSON(ctx, requestURL, apiKey, body, "anthropic", &parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return "", errors.New(strings.TrimSpace(parsed.Error.Message))
	}
	texts := make([]string, 0, len(parsed.Content))
	for _, block := range parsed.Content {
		if strings.TrimSpace(block.Type) == contentPartTypeText && strings.TrimSpace(block.Text) != "" {
			texts = append(texts, strings.TrimSpace(block.Text))
		}
	}
	if len(texts) == 0 {
		return "", errors.New("远程视觉模型没有返回文本内容")
	}
	return strings.Join(texts, "\n"), nil
}

// postVisionJSON 发送一次非流式 JSON POST 并解析响应，统一处理鉴权头与错误响应体。
func postVisionJSON(
	ctx context.Context,
	requestURL string,
	apiKey string,
	body map[string]any,
	provider string,
	target any,
) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, remoteVisionTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.EqualFold(strings.TrimSpace(provider), "anthropic") {
		ApplyAnthropicCompatibleAuthHeaders(httpReq, apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		httpReq.Header.Set("User-Agent", AnthropicClaudeCodeUserAgent)
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("User-Agent", ClaudeCodeUserAgent)
	}

	client := netproxy.NewHTTPClient(remoteVisionTimeout)
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("远程视觉模型请求失败 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("解析远程视觉模型响应失败: %v", err)
	}
	return nil
}

// readVisionRefFile 读取落盘图片，返回内容与 MIME 类型。
func readVisionRefFile(path string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, "", errors.New("图片路径为空")
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, "", err
	}
	return payload, normalizeImageMIMEType("", trimmed, payload), nil
}

// normalizeVisionTextContent 提取 OpenAI chat/completions 的文本内容（兼容字符串与内容块数组）。
func normalizeVisionTextContent(content any) (string, error) {
	switch typed := content.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return "", errors.New("远程视觉模型没有返回文本内容")
		}
		return text, nil
	case []any:
		texts := make([]string, 0, len(typed))
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(fmt.Sprintf("%v", block["type"])) != contentPartTypeText {
				continue
			}
			if text := strings.TrimSpace(fmt.Sprintf("%v", block["text"])); text != "" {
				texts = append(texts, text)
			}
		}
		if len(texts) == 0 {
			return "", errors.New("远程视觉模型没有返回文本内容")
		}
		return strings.Join(texts, "\n"), nil
	default:
		return "", errors.New("远程视觉模型返回内容格式不支持")
	}
}
