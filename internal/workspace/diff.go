// diff.go 提供统一的文本差异计算：基于行级 diff（lineDiff，见 merge.go）
// 渲染标准 unified diff 文本（@@ 头 + 上下文行 + +/- 行）。
//
// 注意：不使用 diffmatchpatch.PatchToText 做展示——
// 它的 PatchMake 是"滚动上下文"设计，长 equal 段不进 patch，
// 生成的 patch 只覆盖变更附近几行，且内容经 QueryEscape 编码，
// 作为人读的 diff 会丢失内容。这里自渲染，内容原样输出。
package workspace

import (
	"fmt"
	"strings"
)

// DiffResult 描述一次文本差异。
type DiffResult struct {
	DiffString   string
	LinesAdded   int32
	LinesRemoved int32
	Changed      bool
}

// DiffContent 计算 before → after 的 unified diff（normalize 为 LF 后比较）。
// 内容相同返回 Changed=false。
func DiffContent(before string, after string) DiffResult {
	if before == after {
		return DiffResult{Changed: false}
	}
	before = normalizeLF(before)
	after = normalizeLF(after)
	ops, err := lineDiff(before, after)
	if err != nil {
		// lineDiff 失败时退化为"全量替换"展示
		patchText, added, removed := renderUnifiedDiff([]lineOp{
			{kind: lineOpDelete, text: before},
			{kind: lineOpInsert, text: after, pos: 0},
		})
		return DiffResult{DiffString: patchText, LinesAdded: added, LinesRemoved: removed, Changed: true}
	}
	patchText, added, removed := renderUnifiedDiff(ops)
	return DiffResult{
		DiffString:   patchText,
		LinesAdded:   added,
		LinesRemoved: removed,
		Changed:      true,
	}
}

// diffLine 是渲染用的一行。
type diffLine struct {
	kind  string // "ctx" | "del" | "ins"
	text  string
	oldNo int // 1-based；ins 行为 0
	newNo int // 1-based；del 行为 0
}

// renderUnifiedDiff 把行级 ops 渲染为 unified diff 文本（上下文 3 行）。
func renderUnifiedDiff(ops []lineOp) (string, int32, int32) {
	var lines []diffLine
	oldNo, newNo := 1, 1
	var added, removed int32
	for _, op := range ops {
		switch op.kind {
		case lineOpEqual:
			lines = append(lines, diffLine{"ctx", op.text, oldNo, newNo})
			oldNo++
			newNo++
		case lineOpDelete:
			lines = append(lines, diffLine{"del", op.text, oldNo, 0})
			oldNo++
			removed++
		case lineOpInsert:
			lines = append(lines, diffLine{"ins", op.text, 0, newNo})
			newNo++
			added++
		}
	}
	if added == 0 && removed == 0 {
		return "", 0, 0
	}

	// 变更行索引（非上下文行）
	var changeIdx []int
	for i, line := range lines {
		if line.kind != "ctx" {
			changeIdx = append(changeIdx, i)
		}
	}
	const context = 3
	var hunks [][]int
	i := 0
	for i < len(changeIdx) {
		start, end := changeIdx[i], changeIdx[i]
		j := i + 1
		for j < len(changeIdx) && changeIdx[j]-end <= 2*context {
			end = changeIdx[j]
			j++
		}
		hs := start - context
		if hs < 0 {
			hs = 0
		}
		he := end + context + 1
		if he > len(lines) {
			he = len(lines)
		}
		hunks = append(hunks, []int{hs, he})
		i = j
	}

	var sb strings.Builder
	for _, hunk := range hunks {
		hs, he := hunk[0], hunk[1]
		oldStart, newStart := 0, 0
		oldEnd, newEnd := 0, 0
		for k := hs; k < he; k++ {
			if lines[k].oldNo > 0 {
				if oldStart == 0 {
					oldStart = lines[k].oldNo
				}
				oldEnd = lines[k].oldNo
			}
			if lines[k].newNo > 0 {
				if newStart == 0 {
					newStart = lines[k].newNo
				}
				newEnd = lines[k].newNo
			}
		}
		if oldStart == 0 {
			oldStart = 1
		}
		if newStart == 0 {
			newStart = 1
		}
		oldCount := oldEnd - oldStart + 1
		if oldEnd == 0 {
			oldCount = 0
			oldStart = 0 // git 风格：纯新增为 -0,0
		}
		newCount := newEnd - newStart + 1
		if newEnd == 0 {
			newCount = 0
			newStart = 0 // git 风格：纯删除为 +0,0
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for k := hs; k < he; k++ {
			switch lines[k].kind {
			case "ctx":
				sb.WriteString(" ")
			case "del":
				sb.WriteString("-")
			case "ins":
				sb.WriteString("+")
			}
			sb.WriteString(lines[k].text)
			// text 含行尾 \n（splitLinesKeepEnd）；最后一行可能不含，
			// 补一个使 patch 完整。
			if !strings.HasSuffix(lines[k].text, "\n") {
				sb.WriteString("\n")
			}
		}
	}
	return sb.String(), added, removed
}

// normalizeLF 统一换行符为 \n，并统一文件末尾换行（EOF newline）。
// 末尾是否带换行不算内容差异：AI 记录的内容可能与客户端写盘的
// 文件在末尾换行上不一致，统一后避免最后一行被误判为修改。
func normalizeLF(value string) string {
	if !strings.ContainsAny(value, "\r\n") && strings.HasSuffix(value, "\n") {
		return value
	}
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if normalized != "" && !strings.HasSuffix(normalized, "\n") {
		normalized += "\n"
	}
	return normalized
}

// ClassifyStatus 根据目标轮次内容与磁盘内容分类文件状态。
func ClassifyStatus(turnContent string, diskContent string, fileExistsOnDisk bool) string {
	switch {
	case !fileExistsOnDisk && turnContent != "":
		return "deleted"
	case fileExistsOnDisk && turnContent == "":
		return "added"
	case fileExistsOnDisk && turnContent != "" && diskContent == turnContent:
		return "unchanged"
	case fileExistsOnDisk && turnContent != "" && diskContent != turnContent:
		return "modified"
	default:
		return "unchanged"
	}
}
