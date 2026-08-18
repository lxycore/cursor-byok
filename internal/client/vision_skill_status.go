// vision_skill_status.go - 返回本机 ds-vision-skill 的视觉通道状态（提供商/baseURL/模型/APIKey）。
package client

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// VisionChannelStatus 描述本机 ds-vision-skill 的一个视觉通道。
type VisionChannelStatus struct {
	// Channel 表示通道名：glm / glm-thinking / custom / baidu-ocr / mineru。
	Channel string `json:"channel"`
	// Name 表示通道的展示名。
	Name string `json:"name"`
	// Provider 表示模型服务提供商。
	Provider string `json:"provider"`
	// BaseURL 表示通道的 API 地址（chat/completions 或端点）。
	BaseURL string `json:"baseURL"`
	// Model 表示默认模型标识。
	Model string `json:"model"`
	// APIKeySet 表示所需的 APIKey（含 secret）是否已配置。
	APIKeySet bool `json:"apiKeySet"`
	// Configured 表示该通道是否完整可用（glm 需 key；custom 需 baseURL+key+model）。
	Configured bool `json:"configured"`
}

// VisionSkillStatusResult 描述本机 ds-vision-skill 的整体状态。
type VisionSkillStatusResult struct {
	// SkillDir 表示 ds-vision-skill 的安装目录。
	SkillDir string `json:"skillDir"`
	// DataDir 表示识别数据根目录。
	DataDir string `json:"dataDir"`
	// Channels 表示各视觉通道的状态。
	Channels []VisionChannelStatus `json:"channels"`
}

// GetVisionSkillStatus 返回本机 ds-vision-skill 的通道状态，供前端“本机识别”展示。
func (s *ProxyService) GetVisionSkillStatus() VisionSkillStatusResult {
	return buildVisionSkillStatus()
}

func buildVisionSkillStatus() VisionSkillStatusResult {
	glmKey := readUserEnv("GLM_API_KEY")
	customBaseURL := readUserEnv("VISION_CUSTOM_BASE_URL")
	customModel := readUserEnv("VISION_CUSTOM_MODEL")
	customKey := readUserEnv("VISION_CUSTOM_API_KEY")
	baiduKey := readUserEnv("BAIDU_API_KEY")
	baiduSecret := readUserEnv("BAIDU_SECRET_KEY")
	mineruToken := readUserEnv("MINERU_TOKEN")

	channels := []VisionChannelStatus{
		{
			Channel:   "glm",
			Name:      "GLM-4V-Flash（简单识图，免费）",
			Provider:  "智谱 GLM",
			BaseURL:   "https://open.bigmodel.cn/api/paas/v4/chat/completions",
			Model:     "glm-4v-flash",
			APIKeySet: glmKey != "",
			Configured: glmKey != "",
		},
		{
			Channel:    "glm-thinking",
			Name:       "GLM-4.1V-Thinking-Flash（复杂推理，免费）",
			Provider:   "智谱 GLM",
			BaseURL:    "https://open.bigmodel.cn/api/paas/v4/chat/completions",
			Model:      "glm-4.1v-thinking-flash",
			APIKeySet:  glmKey != "",
			Configured: glmKey != "",
		},
		{
			Channel:    "custom",
			Name:       "自定义中转 / 私有端点",
			Provider:   "自定义 OpenAI 兼容",
			BaseURL:    customBaseURL,
			Model:      customModel,
			APIKeySet:  customKey != "",
			Configured: customBaseURL != "" && customModel != "" && customKey != "",
		},
		{
			Channel:    "baidu-ocr",
			Name:       "百度 OCR（文字识别）",
			Provider:   "百度智能云",
			BaseURL:    "https://aip.baidubce.com/rest/2.0/ocr/v1/general_basic",
			Model:      "general_basic（高精度 accurate_basic）",
			APIKeySet:  baiduKey != "" && baiduSecret != "",
			Configured: baiduKey != "" && baiduSecret != "",
		},
		{
			Channel:    "mineru",
			Name:       "MinerU（PDF/文档解析）",
			Provider:   "MinerU",
			BaseURL:    "mineru-open-api",
			Model:      "flash-extract（extract 需 token）",
			APIKeySet:  mineruToken != "",
			Configured: true, // flash 模式免 token，extract 模式需要 MINERU_TOKEN
		},
	}
	return VisionSkillStatusResult{
		SkillDir: firstNonEmptyTrimmed(os.Getenv("DS_VISION_SKILL_DIR"), `D:\ds-vision-skill`),
		DataDir:  firstNonEmptyTrimmed(os.Getenv("DS_VISION_DATA_DIR"), `D:\ds-vision-data`),
		Channels: channels,
	}
}

// readUserEnv 先读当前进程环境变量，读不到时回退到用户级环境变量（HKCU\Environment）。
func readUserEnv(name string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
