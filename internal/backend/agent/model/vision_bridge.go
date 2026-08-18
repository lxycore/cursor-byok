// vision_bridge.go - converts image content parts into text via ds-vision-skill.
//
// When a channel has VisionEnabled=true, pasted/dragged images are:
//   1) persisted to D:\ds-vision-data\pasted\dsv_<ts>_<n>.<ext>
//   2) sent to scripts\ds-vision.ps1 (GLM / OCR / MinerU) via a temp inputs file
//   3) replaced in the outgoing message with the recognized text summary plus
//      the real file paths, so a text-only model (DeepSeek) can answer and the
//      user can find the original images.
//
// Env overrides: DS_VISION_SKILL_DIR, DS_VISION_DATA_DIR, DS_VISION_POWERSHELL.

package modeladapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	visionBridgeDefaultSkillDir   = `D:\ds-vision-skill`
	visionBridgeDefaultDataDir    = `D:\ds-vision-data`
	visionBridgeDefaultPowerShell = "powershell.exe"
	visionBridgeTimeout           = 150 * time.Second
)

// visionResultCache 缓存"图片内容+提示"对应的识别摘要，避免 agent 循环重放同一
// 图片消息时反复落盘、反复调用识别引擎。
var (
	visionResultCacheMu sync.Mutex
	visionResultCache   = make(map[string]string)
)

// imageContentHash 返回图片内容的短哈希，用于按内容去重命名保存文件。
func imageContentHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:8])
}

// visionCacheKey 组合所有图片路径与提示文本，生成结果缓存键。
func visionCacheKey(refs []map[string]string, prompt string) string {
	parts := make([]string, 0, len(refs)+1)
	for _, ref := range refs {
		parts = append(parts, ref["value"])
	}
	parts = append(parts, prompt)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func visionCacheGet(key string) string {
	visionResultCacheMu.Lock()
	defer visionResultCacheMu.Unlock()
	return visionResultCache[key]
}

func visionCacheSet(key, value string) {
	visionResultCacheMu.Lock()
	defer visionResultCacheMu.Unlock()
	if len(visionResultCache) >= 512 {
		visionResultCache = make(map[string]string)
	}
	visionResultCache[key] = value
}

type visionBridgeResult struct {
	Result string `json:"result"`
}

// ConvertImageMessages returns a copy of messages where image content parts are
// replaced by text (vision summary + saved file paths). Messages without images
// are returned unchanged. Failures degrade to a visible note instead of
// breaking the chat.
func ConvertImageMessages(ctx context.Context, messages []Message) []Message {
	if len(messages) == 0 {
		return messages
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
		out = append(out, convertImageMessage(ctx, msg, orchestrator, pwsh, pastedDir, tmpDir))
	}
	return out
}

func convertImageMessage(
	ctx context.Context,
	msg Message,
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
		resultText = runVisionOrchestrator(ctx, orchestrator, pwsh, tmpDir, persisted.refs, prompt)
		if !strings.HasPrefix(resultText, "[图片自动识别失败") {
			visionCacheSet(cacheKey, resultText)
		}
	}
	return buildVisionResultMessage(msg, persisted.textParts, persisted.pathLines, resultText)
}

// visionPersistedRefs 保存图片落盘后的引用与展示信息。
type visionPersistedRefs struct {
	// refs 是传给识别引擎的图片引用列表（source=path）。
	refs []map[string]string
	// pathLines 是写入识别结果的消息文本中的图片路径行。
	pathLines []string
	// textParts 是消息中原始文本内容。
	textParts string
}

// persistVisionImageRefs 把消息中的图片内容按内容哈希落盘，并返回引用列表。
// 返回 false 表示没有任何图片可保存。
func persistVisionImageRefs(msg Message, pastedDir string) (visionPersistedRefs, bool) {
	textParts := strings.TrimSpace(collapseTextContentParts(msg.ContentParts))
	if textParts == "" {
		textParts = strings.TrimSpace(msg.Content)
	}

	refs := make([]map[string]string, 0, len(msg.ContentParts))
	pathLines := make([]string, 0, len(msg.ContentParts))
	index := 0
	for _, part := range msg.ContentParts {
		if normalizeContentPartType(part.Type) != contentPartTypeImage {
			continue
		}
		index++
		payload, mime, err := resolveImageContent(part.Image)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(pastedDir, 0o755); err != nil {
			continue
		}
		// 按内容哈希命名并去重：同一张图被 agent 循环重放多次时只保留一份。
		filePath := filepath.Join(pastedDir, fmt.Sprintf("dsv_%s%s", imageContentHash(payload), visionMimeToExt(mime)))
		if _, statErr := os.Stat(filePath); os.IsNotExist(statErr) {
			if err := os.WriteFile(filePath, payload, 0o644); err != nil {
				continue
			}
		}
		refs = append(refs, map[string]string{"source": "path", "value": filePath})
		pathLines = append(pathLines, fmt.Sprintf("- 图片%d: %s", index, filePath))
	}

	if len(refs) == 0 {
		return visionPersistedRefs{}, false
	}
	return visionPersistedRefs{refs: refs, pathLines: pathLines, textParts: textParts}, true
}

// buildVisionResultMessage 把识别摘要拼接成最终发给主模型的文本消息。
func buildVisionResultMessage(msg Message, textParts string, pathLines []string, resultText string) Message {
	var b strings.Builder
	if textParts != "" {
		b.WriteString(textParts)
		b.WriteString("\n\n")
	}
	b.WriteString("[图片自动识别结果]\n")
	b.WriteString("已保存的图片文件（来自粘贴/拖拽，不在工作区，请勿到项目目录中寻找）:\n")
	b.WriteString(strings.Join(pathLines, "\n"))
	b.WriteString("\n\n图片内容摘要:\n")
	b.WriteString(resultText)

	msg.Content = b.String()
	msg.ContentParts = nil
	return msg
}

func runVisionOrchestrator(
	ctx context.Context,
	orchestrator string,
	pwsh string,
	tmpDir string,
	refs []map[string]string,
	prompt string,
) string {
	payload, err := json.Marshal(refs)
	if err != nil {
		return fmt.Sprintf("[图片自动识别失败: %v]", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Sprintf("[图片自动识别失败: 无法创建临时目录 %v]", err)
	}
	inputsFile := filepath.Join(tmpDir, fmt.Sprintf("dsv-inputs-%d-%d.json", os.Getpid(), time.Now().UnixNano()))
	if err := os.WriteFile(inputsFile, payload, 0o644); err != nil {
		return fmt.Sprintf("[图片自动识别失败: 无法写入临时文件 %v]", err)
	}
	defer os.Remove(inputsFile)

	callCtx, cancel := context.WithTimeout(ctx, visionBridgeTimeout)
	defer cancel()
	cmd := exec.CommandContext(callCtx, pwsh,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", orchestrator,
		"-InputsFile", inputsFile,
		"-Prompt", prompt,
		"-Mode", "auto",
		"-Json",
	)
	applyHideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Sprintf("[图片自动识别失败: %s]", detail)
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "[图片自动识别失败: 空输出]"
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "[图片自动识别失败: 输出格式异常]"
	}
	var parsed visionBridgeResult
	if err := json.Unmarshal([]byte(text[start:end+1]), &parsed); err != nil {
		return fmt.Sprintf("[图片自动识别失败: 解析输出 %v]", err)
	}
	if strings.TrimSpace(parsed.Result) == "" {
		return "[图片自动识别失败: 空结果]"
	}
	return parsed.Result
}

func visionEnvOr(name string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func visionMimeToExt(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.Contains(m, "jpeg"):
		return ".jpg"
	case strings.Contains(m, "png"):
		return ".png"
	case strings.Contains(m, "webp"):
		return ".webp"
	case strings.Contains(m, "gif"):
		return ".gif"
	case strings.Contains(m, "bmp"):
		return ".bmp"
	case strings.Contains(m, "tiff"):
		return ".tiff"
	default:
		return ".png"
	}
}
