// merge.go 实现行级三方合并（diff3 风格）与冲突检测。
//
// 语义：base 是 AI 读取文件时看到的内容，theirs 是 AI 想写入的内容，
// ours 是当前磁盘上的实际内容（可能被用户或其他对话修改）。
//
//   - base == ours      → 磁盘无外部修改，直接采用 theirs（快路径）
//   - base == theirs    → AI 的修改等于没有变化，保留磁盘 ours
//   - 行级不重叠        → 自动合并：在 base 坐标上同时应用双方修改
//   - 行级重叠          → 返回 line_overlap 冲突，不产出合并结果
//   - base 空而 ours 非空 → file_created 冲突（同名文件已被别人创建）
//   - ours 不存在       → file_deleted 冲突（目标文件已从磁盘消失）
package workspace

import (
	"fmt"
	"strings"
	"time"
)

// MergeResult 描述一次三方合并的结果。
type MergeResult struct {
	// Merged 是合并后的内容（仅在无冲突时非空）。
	Merged string
	// Conflict 非 nil 表示存在冲突（line_overlap / file_created / file_deleted）。
	Conflict *LineConflict
}

// lineOpKind 表示行级 diff 操作类型。
type lineOpKind string

const (
	lineOpEqual  lineOpKind = "equal"
	lineOpDelete lineOpKind = "delete"
	lineOpInsert lineOpKind = "insert"
)

// lineOp 是行级 diff 的一个操作。
type lineOp struct {
	kind lineOpKind
	// equal/delete：base 中的行（含行尾）；insert：target 中的行（含行尾）。
	text string
	// insert 操作记录插入位置（base 行坐标，即在 base 第 pos 行之前插入）。
	pos int
	// delete 操作记录被删除的 base 行号。
	line int
}

// lineDiff 计算 base → target 的行级 diff。
// 实现：Myers O(ND) 算法（git 同款），直接作用于行序列，天然按行对齐，
// 不存在字符级段边界切破行的问题。先裁剪公共前后缀控制规模。
func lineDiff(base string, target string) ([]lineOp, error) {
	baseLines := splitLinesKeepEnd(base)
	targetLines := splitLinesKeepEnd(target)

	// 裁剪公共前缀
	prefix := 0
	for prefix < len(baseLines) && prefix < len(targetLines) && baseLines[prefix] == targetLines[prefix] {
		prefix++
	}
	// 裁剪公共后缀
	suffix := 0
	for suffix < len(baseLines)-prefix && suffix < len(targetLines)-prefix &&
		baseLines[len(baseLines)-1-suffix] == targetLines[len(targetLines)-1-suffix] {
		suffix++
	}

	var ops []lineOp
	for i := 0; i < prefix; i++ {
		ops = append(ops, lineOp{kind: lineOpEqual, text: baseLines[i]})
	}
	midBase := baseLines[prefix : len(baseLines)-suffix]
	midTarget := targetLines[prefix : len(targetLines)-suffix]
	midOps := myersDiff(midBase, midTarget, prefix)
	ops = append(ops, midOps...)
	for i := len(baseLines) - suffix; i < len(baseLines); i++ {
		ops = append(ops, lineOp{kind: lineOpEqual, text: baseLines[i]})
	}
	return ops, nil
}

// myersDiff 计算 a → b 的 Myers 最短编辑脚本。
// aBase 是 a 在完整行序列中的起始偏移（行号）。
// 行数过大（差异面巨大）时退化为整文件替换，避免 O(D·(N+M)) 内存爆炸。
func myersDiff(a []string, b []string, aBase int) []lineOp {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	if n*m > myersWorkLimit {
		// 退化：整段替换
		var ops []lineOp
		for i := 0; i < n; i++ {
			ops = append(ops, lineOp{kind: lineOpDelete, text: a[i], line: aBase + i})
		}
		for j := 0; j < m; j++ {
			ops = append(ops, lineOp{kind: lineOpInsert, text: b[j], pos: aBase})
		}
		return ops
	}

	max := n + m
	offset := max
	v := make([]int, 2*max+1)
	var trace [][]int
	var endD int

	// 正向搜索
	for d := 0; d <= max; d++ {
		vCopy := make([]int, len(v))
		copy(vCopy, v)
		trace = append(trace, vCopy)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				endD = d
				return myersBacktrack(a, b, trace, offset, endD, aBase)
			}
		}
	}
	// 不可达（理论上不会）
	var ops []lineOp
	for i := 0; i < n; i++ {
		ops = append(ops, lineOp{kind: lineOpDelete, text: a[i], line: aBase + i})
	}
	for j := 0; j < m; j++ {
		ops = append(ops, lineOp{kind: lineOpInsert, text: b[j], pos: aBase})
	}
	return ops
}

// myersWorkLimit 限制 Myers 的工作量（行数乘积），超出退化为整段替换。
const myersWorkLimit = 4_000_000

// myersBacktrack 从 trace 回溯编辑脚本。
func myersBacktrack(a []string, b []string, trace [][]int, offset int, endD int, aBase int) []lineOp {
	x, y := len(a), len(b)
	var rev []lineOp
	for d := endD; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			rev = append(rev, lineOp{kind: lineOpEqual, text: a[x-1], line: aBase + x - 1})
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				rev = append(rev, lineOp{kind: lineOpInsert, text: b[y-1], pos: aBase + prevX})
				y--
			} else {
				rev = append(rev, lineOp{kind: lineOpDelete, text: a[x-1], line: aBase + x - 1})
				x--
			}
		}
	}
	// 反转
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// splitLinesKeepEnd 按行切分文本，保留行尾换行符；最后一行若无换行则原样保留。
func splitLinesKeepEnd(text string) []string {
	if text == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines = append(lines, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

func changedLineSetOf(ops []lineOp) map[int]bool {
	changed := make(map[int]bool)
	for _, op := range ops {
		if op.kind == lineOpDelete {
			changed[op.line+1] = true // 转 1-based
		}
	}
	return changed
}

// insertedLinesByPos 返回 insert 操作按插入位置分组的行文本。
func insertedLinesByPos(ops []lineOp) map[int][]string {
	result := make(map[int][]string)
	for _, op := range ops {
		if op.kind == lineOpInsert {
			result[op.pos] = append(result[op.pos], op.text)
		}
	}
	return result
}

// Merge3 执行三方合并。详见文件头注释。
// 所有比较在 normalizeLF 后进行：Windows 下客户端写盘会把 \n 转成 \r\n，
// 行尾差异不算内容修改，也不应触发冲突。
func Merge3(base string, ours string, theirs string) (*MergeResult, error) {
	base = normalizeLF(base)
	ours = normalizeLF(ours)
	theirs = normalizeLF(theirs)
	if base == ours {
		// 磁盘未被外部修改：直接采用 AI 意图
		return &MergeResult{Merged: theirs}, nil
	}
	if base == theirs {
		// AI 的修改没有实际变化：保留磁盘现状
		return &MergeResult{Merged: ours}, nil
	}
	// 文件级场景
	if strings.TrimSpace(base) == "" && strings.TrimSpace(ours) != "" {
		// AI 认为文件不存在（新建），但磁盘已有内容
		return &MergeResult{Conflict: &LineConflict{
			ConflictType:   "file_created",
			BaseContent:    base,
			DiskContent:    ours,
			AIAfterContent: theirs,
			DetectedAt:     nowRFC3339(),
		}}, nil
	}
	if strings.TrimSpace(ours) == "" && strings.TrimSpace(base) != "" {
		// 磁盘上文件已不存在
		return &MergeResult{Conflict: &LineConflict{
			ConflictType:   "file_deleted",
			BaseContent:    base,
			DiskContent:    ours,
			AIAfterContent: theirs,
			DetectedAt:     nowRFC3339(),
		}}, nil
	}

	theirsOps, err := lineDiff(base, theirs)
	if err != nil {
		return nil, err
	}
	oursOps, err := lineDiff(base, ours)
	if err != nil {
		return nil, err
	}

	theirsDel := changedLineSetOf(theirsOps)
	oursDel := changedLineSetOf(oursOps)

	// 重叠检测
	var overlap []int
	for line := range oursDel {
		if theirsDel[line] {
			overlap = append(overlap, line)
		}
	}
	if len(overlap) > 0 {
		return &MergeResult{Conflict: &LineConflict{
			ConflictType:   "line_overlap",
			BaseContent:    base,
			DiskContent:    ours,
			AIAfterContent: theirs,
			OverlapRange:   formatLineSet(overlap),
			DetectedAt:     nowRFC3339(),
		}}, nil
	}

	// 无重叠：合并。按 base 行位置逐位输出：
	//   - 位置 i 的插入（ours 优先，theirs 其次）总是输出；
	//   - base 行 i 若被任一方删除（替换），不输出原行（替换行已作为插入输出）；
	//   - 否则输出 base 原行。
	theirsIns := insertedLinesByPos(theirsOps)
	oursIns := insertedLinesByPos(oursOps)

	baseLines := splitLinesKeepEnd(base)
	var builder strings.Builder
	builder.Grow(len(base) + len(theirs) + len(ours) + 64)

	for pos := 0; pos <= len(baseLines); pos++ {
		for _, line := range oursIns[pos] {
			builder.WriteString(line)
		}
		for _, line := range theirsIns[pos] {
			builder.WriteString(line)
		}
		if pos >= len(baseLines) {
			continue
		}
		lineNo := pos + 1 // 1-based
		if oursDel[lineNo] || theirsDel[lineNo] {
			continue // 该行被替换/删除，原行不输出
		}
		builder.WriteString(baseLines[pos])
	}

	return &MergeResult{Merged: builder.String()}, nil
}

// formatLineSet 把行号集合格式化为 "L10-L15,L20" 形式。
func formatLineSet(lines []int) string {
	if len(lines) == 0 {
		return ""
	}
	sorted := append([]int(nil), lines...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	var parts []string
	start := sorted[0]
	prev := sorted[0]
	for i := 1; i < len(sorted); i++ {
		if sorted[i] > prev+1 {
			if start == prev {
				parts = append(parts, fmt.Sprintf("L%d", start))
			} else {
				parts = append(parts, fmt.Sprintf("L%d-L%d", start, prev))
			}
			start = sorted[i]
		}
		prev = sorted[i]
	}
	if start == prev {
		parts = append(parts, fmt.Sprintf("L%d", start))
	} else {
		parts = append(parts, fmt.Sprintf("L%d-L%d", start, prev))
	}
	return strings.Join(parts, ",")
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
