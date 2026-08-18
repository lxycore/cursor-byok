<script setup>
import { computed, nextTick, ref, watchEffect } from "vue";

const props = defineProps({
  diff: { type: Object, default: null },
  loading: { type: Boolean, default: false },
  error: { type: String, default: "" },
});
const emit = defineEmits(["close"]);

// ── Helpers ──────────────────────────────────────────
// diffString 由后端直接生成 unified patch 文本（不做 URL 解码，
// 避免把内容里的字面 %XX 序列破坏掉）。

/** Parse a unified-diff string into structured lines.
 *  hunk 头（@@ -0,0 +1,44 @@ 之类）是 diff 文件格式坐标，普通用户看不懂
 *  也没有信息量，直接跳过不渲染，只保留 +/- 变更行与上下文行。
 */
function parseDiff(ds) {
  if (!ds) return [];
  const lines = ds.split("\n");
  const out = [];
  let o = 0, n = 0;
  for (const raw of lines) {
    const text = raw;
    if (text.startsWith("@@")) {
      const m = text.match(/@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/);
      if (m) { o = +m[1] - 1; n = +m[2] - 1; }
      continue; // 跳过 hunk 头，不渲染
    } else if (text.startsWith("---") || text.startsWith("+++")) {
      continue; // 跳过文件头
    } else {
      const add = text.startsWith("+"), del = text.startsWith("-");
      if (add) n++;
      if (del) o++;
      if (!add && !del) { o++; n++; }
      out.push({ t: add ? "a" : del ? "d" : "c", text, o: add ? null : o, n: del ? null : n, id: out.length });
    }
  }
  return out;
}

// ── State ────────────────────────────────────────────
const files = computed(() => props.diff?.files?.filter(f => f.status !== "unchanged") ?? []);
const fileQ = ref("");
const filtered = computed(() => {
  const q = fileQ.value.trim().toLowerCase();
  return !q ? files.value : files.value.filter(f => f.filePath.toLowerCase().includes(q));
});
const selIdx = ref(0);
const selected = computed(() => {
  const list = filtered.value;
  if (!list.length) return null;
  selIdx.value = Math.min(selIdx.value, list.length - 1);
  return list[selIdx.value];
});

const parsed = computed(() => (selected.value ? parseDiff(selected.value.diffString) : []));

// ── Search ────────────────────────────────────────────
const sq = ref("");
const mc = ref(false);
const mi = ref(0);
const mcnt = ref(0);
const matches = computed(() => {
  const q = sq.value.trim();
  if (!q || !parsed.value.length) return [];
  const cs = mc.value;
  const r = [];
  parsed.value.forEach((l, i) => {
    if (l.t === "h" || l.t === "m") return;
    const body = "ad".includes(l.t) ? l.text.substring(1) : l.text;
    if (cs ? body.includes(q) : body.toLowerCase().includes(q.toLowerCase())) r.push(i);
  });
  return r;
});
watchEffect(() => {
  mcnt.value = matches.value.length;
  if (mi.value >= mcnt.value) mi.value = Math.max(0, mcnt.value - 1);
});
function go(i) {
  if (i < 0 || i >= matches.value.length) return;
  mi.value = i;
  nextTick(() => document.getElementById(`dl-${matches.value[i]}`)?.scrollIntoView({ block: "center" }));
}
function next() { if (mcnt.value) go((mi.value + 1) % mcnt.value); }
function prev()  { if (mcnt.value) go((mi.value - 1 + mcnt.value) % mcnt.value); }
function onKey(e) {
  if (e.key === "Enter") { e.preventDefault(); e.shiftKey ? prev() : next(); }
  if (e.key === "Escape") sq.value = "";
}
function highlight(body, q, cs) {
  if (!q || !body) return [{ text: body ?? "", hl: false }];
  const needle = cs ? q : q.toLowerCase();
  const hay = cs ? body : body.toLowerCase();
  const parts = [];
  let i = hay.indexOf(needle), last = 0;
  while (i >= 0) {
    if (i > last) parts.push({ text: body.slice(last, i), hl: false });
    parts.push({ text: body.slice(i, i + q.length), hl: true });
    last = i + q.length; i = hay.indexOf(needle, last);
  }
  if (last < body.length) parts.push({ text: body.slice(last), hl: false });
  return parts.length ? parts : [{ text: body, hl: false }];
}

// ── Stats ────────────────────────────────────────────
const stats = computed(() => {
  const s = { added: 0, modified: 0, deleted: 0 };
  for (const f of files.value) {
    if (f.status === "added") s.added++;
    else if (f.status === "modified") s.modified++;
    else if (f.status === "deleted") s.deleted++;
  }
  return s;
});
const fi = (s) => ({
  added:    { i: "A", c: "text-[#22c55e] bg-[#0a2e1a]" },
  modified: { i: "M", c: "text-[#60a5fa] bg-[#1a2a4a]" },
  deleted:  { i: "D", c: "text-[#f87171] bg-[#4a1a1a]" },
})[s] ?? { i: "", c: "text-[#888] bg-[#252525]" };

// ── Collapse ──────────────────────────────────────────
const collapsedFiles = ref(new Set());
function toggleFile(fp) {
  if (collapsedFiles.value.has(fp)) collapsedFiles.value.delete(fp);
  else collapsedFiles.value.add(fp);
}
const collapsedHunks = ref(new Set());
function toggleHunk(id) {
  if (collapsedHunks.value.has(id)) collapsedHunks.value.delete(id);
  else collapsedHunks.value.add(id);
}
</script>

<template>
  <div class="fixed inset-0 z-[999] flex items-center justify-center bg-black/60" @click.self="emit('close')">
    <div class="flex w-[92vw] max-w-[1400px] h-[88vh] flex-col overflow-hidden rounded-lg border border-[#3a3a3a] bg-[#0d0d0d] shadow-2xl">

      <!-- ── Top bar ──────────────────────────── -->
      <div class="flex items-center justify-between gap-3 border-b border-[#2a2a2a] px-4 py-3">
        <div class="flex items-center gap-3 min-w-0">
          <h3 class="text-sm font-semibold text-white/90 truncate">{{ "文件变更" }}</h3>
          <span v-if="diff?.turnSeq" class="rounded bg-[#1a2a4a] px-2 py-0.5 text-[11px] text-[#60a5fa]">{{ "轮次" }} {{ diff.turnSeq }}</span>
          <span v-if="diff?.modelName" class="rounded bg-[#1a2a4a] px-2 py-0.5 text-[11px] text-[#60a5fa]">{{ diff.modelName }}</span>
          <span v-if="stats.added>0" class="rounded bg-[#0a2e1a] px-2 py-0.5 text-[11px] text-[#22c55e]">+{{ stats.added }}</span>
          <span v-if="stats.modified>0" class="rounded bg-[#1a2a4a] px-2 py-0.5 text-[11px] text-[#60a5fa]">~{{ stats.modified }}</span>
          <span v-if="stats.deleted>0" class="rounded bg-[#2a1313] px-2 py-0.5 text-[11px] text-[#f87171]">-{{ stats.deleted }}</span>
        </div>
        <button class="rounded px-3 py-1 text-xs text-[#888] transition-colors hover:bg-[#252525] hover:text-[#e5e5e5]" @click="emit('close')">{{ "关闭" }}</button>
      </div>

      <!-- ── Body ─────────────────────────────── -->
      <template v-if="loading">
        <div class="flex flex-1 items-center justify-center"><span class="text-xs text-[#686868]">{{ "加载中..." }}</span></div>
      </template>
      <template v-else-if="error">
        <div class="flex flex-1 items-center justify-center"><span class="text-xs text-[#fca5a5]">{{ error }}</span></div>
      </template>
      <template v-else-if="!files.length">
        <div class="flex flex-1 items-center justify-center"><span class="text-xs text-[#686868]">{{ "无文件变更" }}</span></div>
      </template>
      <template v-else>
        <div class="flex flex-1 min-h-0">

          <!-- ── File sidebar ────────────────── -->
          <div class="w-64 shrink-0 flex flex-col border-r border-[#2a2a2a] bg-[#0d0d0d]">
            <div class="shrink-0 border-b border-[#2a2a2a] px-3 py-2">
              <input v-model="fileQ" type="text"
                :placeholder="'搜索文件...'"
                class="w-full rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-1.5 text-xs text-[#e5e5e5] placeholder-[#555] outline-none focus:border-[#10AD5D]" />
            </div>
            <div class="flex-1 overflow-y-auto">
              <div v-if="filtered.length===0 && fileQ.trim()" class="px-3 py-4 text-center text-[10px] text-[#555]">{{ "无匹配" }}</div>
              <div v-for="(f, i) in filtered" :key="f.filePath"
                class="flex cursor-pointer items-center gap-2 px-3 py-2 text-xs border-b border-[#1a1a1a] last:border-0 transition-colors"
                :class="i===selIdx ? 'bg-[#1a1a1a]' : 'hover:bg-[#151515]'" @click="selIdx=i">
                <span class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded text-[9px] font-bold" :class="fi(f.status).c">{{ fi(f.status).i }}</span>
                <span class="truncate text-[#ccc]" :title="f.filePath">{{ f.filePath.split(/[/\\]/).pop() }}</span>
                <span class="ml-auto shrink-0 text-[10px] whitespace-nowrap">
                  <span v-if="f.linesAdded" class="text-[#22c55e]">+{{ f.linesAdded }}</span>
                  <span v-if="f.linesAdded && f.linesRemoved" class="text-[#555] mx-0.5">/</span>
                  <span v-if="f.linesRemoved" class="text-[#f87171]">-{{ f.linesRemoved }}</span>
                </span>
              </div>
            </div>
          </div>

          <!-- ── Diff panel ──────────────────── -->
          <div class="flex flex-1 flex-col min-w-0 bg-[#0d0d0d]">
            <!-- Search bar -->
            <div v-if="selected" class="shrink-0 flex items-center gap-2 border-b border-[#2a2a2a] px-3 py-2">
              <span class="text-[11px] text-[#555]">{{ "搜索" }}:</span>
              <input v-model="sq" type="text"
                :placeholder="'在 diff 中搜索...'"
                class="w-48 rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-1.5 text-xs text-[#e5e5e5] placeholder-[#555] outline-none focus:border-[#10AD5D]"
                @keydown="onKey" />
              <button class="rounded px-2 py-1 text-[11px] font-medium"
                :class="mc ? 'bg-[#10AD5D] text-white' : 'bg-[#252525] text-[#888] hover:bg-[#2a2a2a]'"
                :title="'大小写匹配'" @click="mc = !mc">Aa</button>
              <span v-if="sq.trim() && mcnt>0" class="text-xs text-[#686868] whitespace-nowrap">{{ mi+1 }}/{{ mcnt }}</span>
              <span v-else-if="sq.trim() && mcnt===0" class="text-xs text-[#f87171]">0</span>
              <div v-if="sq.trim() && mcnt>0" class="flex gap-0.5">
                <button class="rounded px-2 py-1 text-xs text-[#888] hover:bg-[#252525]" @click="prev">▲</button>
                <button class="rounded px-2 py-1 text-xs text-[#888] hover:bg-[#252525]" @click="next">▼</button>
              </div>
            </div>

            <!-- Diff content -->
            <div class="flex-1 overflow-y-auto font-mono text-[12px] leading-[1.6]">
              <!-- Single file diff block (GitHub style) -->
              <div v-if="selected" class="border-b border-[#2a2a2a] last:border-0">
                <!-- File header -->
                <div class="sticky top-0 z-10 flex items-center gap-2 border-b border-[#2a2a2a] bg-[#151515] px-3 py-1.5 text-[12px]">
                  <button class="text-[10px] text-[#555] hover:text-[#ccc] transition-colors"
                    @click="toggleFile(selected.filePath)">
                    <span v-if="collapsedFiles.has(selected.filePath)">▶</span>
                    <span v-else>▼</span>
                  </button>
                  <span class="inline-flex h-4 w-4 items-center justify-center rounded text-[8px] font-bold" :class="fi(selected.status).c">{{ fi(selected.status).i }}</span>
                  <span class="text-[#ccc] truncate" :title="selected.filePath">{{ selected.filePath }}</span>
                </div>

                <!-- Diff table (hidden when collapsed) -->
                <div v-if="!collapsedFiles.has(selected.filePath)">
                  <table class="w-full border-collapse">
                    <tbody>
                      <tr v-for="(line, i) in parsed" :id="`dl-${i}`" :key="i"
                        :class="[
                          line.t==='a' ? 'bg-[#0a2818]' : line.t==='d' ? 'bg-[#281515]' : '',
                          line.t==='a'||line.t==='d' ? '' : 'hover:bg-[#121212]',
                          sq.trim() && matches.includes(i) && matches.indexOf(i)===mi ? 'ring-1 ring-inset ring-[#fb923c]' : '',
                        ]">
                        <!-- old line -->
                        <td class="w-[48px] min-w-[48px] select-none text-right text-[11px] py-0 pl-3 pr-1 align-top tabular-nums"
                          :class="line.t==='a' ? 'text-[#3a6a4a]' : line.t==='d' ? 'text-[#7a3a3a]' : 'text-[#404040]'">{{ line.o ?? "" }}</td>
                        <!-- new line -->
                        <td class="w-[48px] min-w-[48px] select-none text-right text-[11px] py-0 pr-2 align-top tabular-nums"
                          :class="line.t==='a' ? 'text-[#3a6a4a]' : line.t==='d' ? 'text-[#7a3a3a]' : 'text-[#404040]'">{{ line.n ?? "" }}</td>
                        <!-- gutter -->
                        <td class="w-[20px] min-w-[20px] select-none text-center py-0 align-top text-[11px]"
                          :class="{
                            'bg-[#0a3a1a] text-[#22c55e]': line.t==='a',
                            'bg-[#3a1717] text-[#f87171]': line.t==='d',
                            'text-[#3a3a3a]': line.t==='c',
                            'text-[#7a7a3a] italic': line.t==='h'||line.t==='m',
                          }">
                          <span v-if="line.t==='a'">+</span>
                          <span v-else-if="line.t==='d'">-</span>
                          <span v-else-if="line.t==='c'">&nbsp;</span>
                          <span v-else>~</span>
                        </td>
                        <!-- content -->
                        <td class="py-0 pr-3 align-top whitespace-pre-wrap break-all text-[12px]"
                          :class="{
                            'text-[#7a9a7a]': line.t==='a',
                            'text-[#9a6a6a]': line.t==='d',
                            'text-[#666]': line.t==='c',
                          }">
                          <!-- search highlight -->
                          <span v-if="sq.trim() && matches.includes(i)">
                            <span v-for="(p, pi) in highlight(
                              'ad'.includes(line.t) ? line.text.substring(1) : line.text,
                              sq.trim(), mc)" :key="pi">
                              <mark v-if="p.hl" class="rounded px-0.5"
                                :class="matches.indexOf(i)===mi ? 'bg-[#fb923c] text-[#1a1a1a]' : 'bg-[#facc15]/40 text-[#d4d4d4]'">{{ p.text }}</mark>
                              <span v-else>{{ p.text }}</span>
                            </span>
                          </span>
                          <span v-else>{{ 'ad'.includes(line.t) ? line.text.substring(1) : line.text }}</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
