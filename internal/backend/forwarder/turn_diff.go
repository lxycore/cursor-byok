// turn_diff.go 仅保留从对话条目提取模型信息的工具函数，
// 供 bridge 层作为 editlog 模型信息缺失时的兜底。
// 文件差异计算已迁移至 internal/workspace（编辑事件日志 + 内容寻址存储）。
package forwarder

import (
	"encoding/json"
	"strings"
)

// ExtractModelNameFromEntries 从对话条目中提取指定轮次所用的模型名。
// 从 run_request 类型的 metadata 中提取 model_name 字段。
func ExtractModelNameFromEntries(entries []HistoryEntry, targetTurnSeq int64) (modelName string, modelID string) {
	for _, entry := range entries {
		if entry.TurnSeq != targetTurnSeq || entry.Kind != "metadata" || entry.Role != "system" {
			continue
		}
		var payload struct {
			Type  string         `json:"type"`
			Value map[string]any `json:"value"`
		}
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if payload.Type == "run_request" {
			if mn, ok := payload.Value["model_name"].(string); ok {
				modelName = strings.TrimSpace(mn)
			}
			if mi, ok := payload.Value["model_id"].(string); ok {
				modelID = strings.TrimSpace(mi)
			}
			if modelName != "" || modelID != "" {
				return
			}
		}
	}
	return "", ""
}
