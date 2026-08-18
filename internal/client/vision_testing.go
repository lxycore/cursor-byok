// vision_testing.go - 模型编辑器里“测试视觉模型”与“本机识别通道测试”的后端实现。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

const (
	// visionTestPrompt 是视觉模型测试使用的提示词。
	visionTestPrompt = "请用一句话描述这张测试图片的内容（用于验证视觉模型是否可用），例如“红绿蓝三色竖条纹”。"
	// visionTestTimeout 是单次视觉测试的超时时间。
	visionTestTimeout = 120 * time.Second
)

// TestVisionResult 定义一次视觉测试的结果。
type TestVisionResult struct {
	OK          bool   `json:"ok"`
	SummaryText string `json:"summaryText"`
	Error       string `json:"error"`
	LatencyMS   int64  `json:"latencyMS"`
	RawResponse string `json:"rawResponse"`
}

// TestRemoteVisionRequest 定义远程视觉模型测试的请求参数。
type TestRemoteVisionRequest struct {
	// ProviderType 表示远程视觉模型的服务提供商类型：openai 或 anthropic。
	ProviderType string `json:"providerType"`
	// BaseURL 表示远程视觉模型的 API 根地址。
	BaseURL string `json:"baseURL"`
	// APIKey 表示远程视觉模型的访问密钥。
	APIKey string `json:"apiKey"`
	// ModelID 表示远程视觉模型的模型标识。
	ModelID string `json:"modelID"`
}

// TestLocalVisionRequest 定义本机 ds-vision 通道测试的请求参数。
type TestLocalVisionRequest struct {
	// Channel 表示本机识别通道：auto / glm / glm-thinking / custom / baidu-ocr / windows-ocr。
	Channel string `json:"channel"`
}

// TestRemoteVision 用一张测试图片验证远程视觉模型是否可用。
func (s *ProxyService) TestRemoteVision(req TestRemoteVisionRequest) TestVisionResult {
	_ = s
	providerType := strings.ToLower(strings.TrimSpace(req.ProviderType))
	if providerType == "" {
		providerType = "openai"
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	modelID := strings.TrimSpace(req.ModelID)
	if baseURL == "" || apiKey == "" || modelID == "" {
		return buildVisionTestFailure("请先填写远程视觉模型的接口地址、访问密钥与模型标识")
	}

	testImage, cleanup, err := createVisionTestImage()
	if err != nil {
		return buildVisionTestFailure("生成测试图片失败: " + err.Error())
	}
	defer cleanup()

	cfg := modeladapter.VisionConfig{
		Mode:         "remote",
		ProviderType: providerType,
		ModelID:      modelID,
		BaseURL:      baseURL,
		APIKey:       apiKey,
	}
	refs := []map[string]string{{"source": "path", "value": testImage}}
	startedAt := time.Now().UTC()
	callCtx, cancel := context.WithTimeout(context.Background(), visionTestTimeout)
	defer cancel()
	text, testErr := modeladapter.RunRemoteVisionErr(callCtx, cfg, visionTestPrompt, refs)
	latencyMS := time.Since(startedAt).Milliseconds()
	if testErr != nil {
		return buildVisionTestFailure(testErr.Error(), latencyMS)
	}
	return TestVisionResult{
		OK:          true,
		SummaryText: fmt.Sprintf("%d ms · %s", latencyMS, truncateVisionText(text, 120)),
		LatencyMS:   latencyMS,
		RawResponse: truncateVisionText(text, 2000),
	}
}

// TestLocalVisionChannel 用一张测试图片验证本机 ds-vision 指定通道是否可用。
func (s *ProxyService) TestLocalVisionChannel(req TestLocalVisionRequest) TestVisionResult {
	_ = s
	channel := strings.ToLower(strings.TrimSpace(req.Channel))
	if channel == "" {
		channel = "auto"
	}
	skillDir := firstNonEmptyTrimmed(os.Getenv("DS_VISION_SKILL_DIR"), `D:\ds-vision-skill`)
	scriptsDir := filepath.Join(skillDir, "scripts")

	testImage, cleanup, err := createVisionTestImage()
	if err != nil {
		return buildVisionTestFailure("生成测试图片失败: " + err.Error())
	}
	defer cleanup()

	var scriptPath string
	var scriptArgs []string
	switch channel {
	case "glm", "glm-thinking", "custom":
		scriptPath = filepath.Join(scriptsDir, "vlm-vision.ps1")
		scriptArgs = []string{"-ImagePath", testImage, "-Prompt", visionTestPrompt, "-Channel", channel, "-Json"}
	case "baidu-ocr":
		scriptPath = filepath.Join(scriptsDir, "baidu-ocr.ps1")
		scriptArgs = []string{"-ImagePath", testImage, "-Json"}
	case "windows-ocr":
		scriptPath = filepath.Join(scriptsDir, "windows-ocr.ps1")
		scriptArgs = []string{"-ImagePath", testImage, "-Json"}
	case "auto":
		scriptPath = filepath.Join(scriptsDir, "ds-vision.ps1")
		scriptArgs = []string{"-ImagePath", testImage, "-Prompt", visionTestPrompt, "-Mode", "auto", "-Json"}
	default:
		return buildVisionTestFailure("不支持的通道: " + channel)
	}

	startedAt := time.Now().UTC()
	callCtx, cancel := context.WithTimeout(context.Background(), visionTestTimeout)
	defer cancel()
	output, err := runVisionTestScript(callCtx, scriptPath, scriptArgs)
	latencyMS := time.Since(startedAt).Milliseconds()
	if err != nil {
		return buildVisionTestFailure(err.Error(), latencyMS)
	}
	resultText, err := extractVisionEnvelopeResult(output)
	if err != nil {
		return buildVisionTestFailure(err.Error(), latencyMS)
	}
	return TestVisionResult{
		OK:          true,
		SummaryText: fmt.Sprintf("%d ms · %s", latencyMS, truncateVisionText(resultText, 120)),
		LatencyMS:   latencyMS,
		RawResponse: truncateVisionText(resultText, 2000),
	}
}

// createVisionTestImage 生成一张红绿蓝三色竖条纹的测试 PNG，返回路径与清理函数。
func createVisionTestImage() (string, func(), error) {
	const width, height = 360, 160
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	colors := []color.RGBA{
		{R: 220, G: 60, B: 60, A: 255},
		{R: 60, G: 180, B: 70, A: 255},
		{R: 60, G: 90, B: 220, A: 255},
	}
	section := width / len(colors)
	for x := 0; x < width; x++ {
		idx := x / section
		if idx >= len(colors) {
			idx = len(colors) - 1
		}
		c := colors[idx]
		for y := 0; y < height; y++ {
			img.SetRGBA(x, y, c)
		}
	}

	dir := filepath.Join(firstNonEmptyTrimmed(os.Getenv("DS_VISION_DATA_DIR"), `D:\ds-vision-data`), "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}, err
	}
	path := filepath.Join(dir, fmt.Sprintf("dsv-test-%d.png", time.Now().UnixNano()))
	file, err := os.Create(path)
	if err != nil {
		return "", func() {}, err
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// runVisionTestScript 以隐藏窗口方式运行 ds-vision 脚本并返回 stdout。
func runVisionTestScript(ctx context.Context, scriptPath string, args []string) (string, error) {
	if strings.TrimSpace(scriptPath) == "" {
		return "", fmt.Errorf("脚本路径为空")
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return "", fmt.Errorf("找不到脚本: %s", scriptPath)
	}
	callCtx, cancel := context.WithTimeout(ctx, visionTestTimeout)
	defer cancel()
	cmdArgs := append([]string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
	}, args...)
	cmd := exec.CommandContext(callCtx, "powershell.exe", cmdArgs...)
	cmd.SysProcAttr = hideWindowSysProcAttr()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("%s", detail)
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "", fmt.Errorf("脚本没有输出")
	}
	return text, nil
}

// extractVisionEnvelopeResult 从 ds-vision 脚本的 JSON 输出中提取 result 字段。
func extractVisionEnvelopeResult(output string) (string, error) {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("输出不是 JSON 格式")
	}
	var envelope struct {
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &envelope); err != nil {
		return "", fmt.Errorf("解析输出失败: %v", err)
	}
	text := strings.TrimSpace(envelope.Result)
	if text == "" {
		if strings.TrimSpace(envelope.Error) != "" {
			return "", fmt.Errorf("%s", strings.TrimSpace(envelope.Error))
		}
		return "", fmt.Errorf("通道未返回内容")
	}
	return text, nil
}

// buildVisionTestFailure 构造失败结果，可选携带耗时。
func buildVisionTestFailure(message string, latencyMS ...int64) TestVisionResult {
	var latency int64
	if len(latencyMS) > 0 {
		latency = latencyMS[0]
	}
	return TestVisionResult{
		OK:          false,
		SummaryText: message,
		Error:       message,
		LatencyMS:   latency,
	}
}

func truncateVisionText(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxLen {
		return value
	}
	return string(runes[:maxLen]) + "…"
}
