<script setup>
import Card from "@/components/ui/Card.vue";
import Button from "@/components/ui/Button.vue";
import FileDiffViewer from "@/components/FileDiffViewer.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import {
  appState,
  fetchRecentTurns,
  fetchTurnDiff,
  clearTurnDiff,
  jumpToTurnById,
  fetchProjectEvents,
} from "@/state/appState";
import { computed, onMounted, onUnmounted, ref } from "vue";

const message = useMessage();
const diffVisible = ref(false);

// 三层树结构：项目 → 对话窗口 → 轮次
const searchQuery = ref("");
const expandedProjects = ref({});
const expandedConversations = ref({});
const expandedEvents = ref({});

// 项目协调事件：按项目根缓存；事件类型 → 文案/样式（中文源文本，翻译由 i18n 目录维护）
const EVENT_LABELS = {
  file_conflict: { zh: "写入冲突（磁盘内容已被 AI 覆盖）", cls: "bg-[#2a1313] text-[#fca5a5]" },
  merge_applied: { zh: "自动合并了并发修改", cls: "bg-[#0a2e1a] text-[#4ade80]" },
  jump_performed: { zh: "执行了版本跳转", cls: "bg-[#1a2a4a] text-[#60a5fa]" },
  write_error: { zh: "文件写入失败", cls: "bg-[#2a1313] text-[#f87171]" },
};

function eventLabel(type) {
  return EVENT_LABELS[type] || { zh: type, cls: "bg-[#252525] text-[#999]" };
}

function projectEventList(projectRoot) {
  return appState.projectEvents[projectRoot] || [];
}

async function toggleProjectEvents(projectRoot) {
  expandedEvents.value[projectRoot] = !expandedEvents.value[projectRoot];
  if (expandedEvents.value[projectRoot]) {
    await fetchProjectEvents(projectRoot, 50);
  }
}

function formatEventTime(isoString) {
  if (!isoString) return "";
  const date = new Date(isoString);
  if (isNaN(date.getTime())) return "";
  const diffMs = Date.now() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return "刚刚";
  if (diffMin < 60) return `${diffMin}分钟前`;
  if (diffMin < 1440) return `${Math.floor(diffMin/60)}小时前`;
  return date.toLocaleDateString();
}

const projectTree = computed(() => {
  const raw = appState.recentTurns;
  const q = searchQuery.value.trim().toLowerCase();
  const tree = {};
  for (const turn of raw) {
    if (q && !matchesSearch(turn, q)) continue;
    const proj = turn.projectName || "(unknown)";
    if (!tree[proj]) tree[proj] = {};
    const cid = turn.conversationId;
    if (!tree[proj][cid]) {
      tree[proj][cid] = {
        convId: cid,
        label: turn.conversationLabel || cid.slice(0, 8),
        isActive: false,
        latestAt: turn.editedAt,
        turnCount: 0,
        turns: [],
      };
    }
    const conv = tree[proj][cid];
    conv.turns.push(turn);
    conv.turnCount++;
    if (turn.isActive) conv.isActive = true;
    if (turn.editedAt > conv.latestAt) conv.latestAt = turn.editedAt;
  }
  for (const proj of Object.keys(tree)) {
    const convs = Object.values(tree[proj]);
    convs.sort((a, b) => b.latestAt.localeCompare(a.latestAt));
    tree[proj] = convs;
    for (const conv of convs) {
      conv.turns.sort((a, b) => b.turnSeq - a.turnSeq);
    }
  }
  return tree;
});

const projectNames = computed(() => {
  return Object.keys(projectTree.value).sort((a, b) => {
    const aActive = projectTree.value[a].some(c => c.isActive);
    const bActive = projectTree.value[b].some(c => c.isActive);
    if (aActive && !bActive) return -1;
    if (!aActive && bActive) return 1;
    const aLatest = projectTree.value[a][0]?.latestAt || "";
    const bLatest = projectTree.value[b][0]?.latestAt || "";
    return bLatest.localeCompare(aLatest);
  });
});

function toggleProject(name) {
  expandedProjects.value[name] = !(expandedProjects.value[name] !== false);
}
function isProjectExpanded(name) {
  return expandedProjects.value[name] !== false;
}
function toggleConversation(proj, convId) {
  const key = proj + "\0" + convId;
  expandedConversations.value[key] = !(expandedConversations.value[key] !== false);
}
function isConversationExpanded(proj, convId) {
  const key = proj + "\0" + convId;
  return expandedConversations.value[key] !== false;
}
function matchesSearch(turn, q) {
  if (!q) return true;
  return (turn.userMessage||'').toLowerCase().includes(q) ||
         (turn.projectName||'').toLowerCase().includes(q) ||
         (turn.filePaths||[]).some(f => f.toLowerCase().includes(q));
}

const isLoading = computed(() => appState.recentTurnsLoading);
const hasError = computed(() => !!appState.recentTurnsError);
const isEmpty = computed(() => !isLoading.value && !hasError.value && appState.recentTurns.length === 0);

function getFileName(fullPath) {
  if (!fullPath) return "";
  const parts = fullPath.split(/[/\\]/);
  return parts[parts.length - 1] || fullPath;
}

// Get CSS class for a file based on its change status
function getFileStyle(turn, filePath) {
  const changes = turn.fileChanges || [];
  const found = Array.isArray(changes) ? changes.find(c => c.filePath === filePath) : null;
  const status = found ? found.status : "";
  switch (status) {
    case "added":    return "bg-[#0a2e1a] text-[#22c55e]";
    case "modified": return "bg-[#1a2a4a] text-[#60a5fa]";
    case "deleted":  return "bg-[#2a1313] text-[#f87171]";
    default:         return "bg-[#252525] text-[#999]";
  }
}

function formatRelativeTime(isoString) {
  if (!isoString) return "";
  const date = new Date(isoString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  const diffHour = Math.floor(diffMs / 3600000);
  const diffDay = Math.floor(diffMs / 86400000);
  if (diffMin < 1) return "刚刚";
  if (diffMin < 60) return `${diffMin}分钟前`;
  if (diffHour < 24) return `${diffHour}小时前`;
  if (diffDay < 7) return `${diffDay}天前`;
  const month = date.getMonth() + 1;
  const day = date.getDate();
  return `${month}/${day}`;
}

function handleRefresh() {
  searchQuery.value = "";
  fetchRecentTurns(100);
}

async function handleJump(turn) {
  if (turn.isActive) {
    message.info("当前版本");
    return;
  }
  const confirmed = await showModal({
    title: "跳转到此版本",
    content: `将文件恢复到：\n"${turn.userMessage}"\n影响 ${turn.filePaths?.length || 0} 个文件`,
  });
  if (!confirmed) return;
  const result = await jumpToTurnById(turn.conversationId, turn.turnSeq);
  if (result.ok) {
    const skipped = result.result?.skippedFiles || [];
    if (skipped.length > 0) {
      message.info(
        `已恢复。${skipped.length} 个共享文件保留了其他对话的修改（未改变）`,
      );
    } else {
      message.success("已恢复");
    }
  } else {
    message.error(result.error || "失败");
  }
}

async function handleViewDiff(turn) {
  diffVisible.value = true;
  const cached = appState.turnDiff;
  if (cached && cached.conversationId === turn.conversationId && cached.turnSeq === turn.turnSeq) {
    return;
  }
  await fetchTurnDiff(turn.conversationId, turn.turnSeq);
}

function handleCloseDiff() {
  diffVisible.value = false;
  clearTurnDiff();
}

// 自动刷新：60 秒轮询 + 窗口聚焦/可见时立即刷新
let refreshTimer = null;
const refreshIntervalMs = 60000;

function refreshIfVisible() {
  if (document.visibilityState === "visible") {
    fetchRecentTurns(100);
  }
}

onMounted(() => {
  refreshTimer = setInterval(refreshIfVisible, refreshIntervalMs);
  window.addEventListener("focus", refreshIfVisible);
  document.addEventListener("visibilitychange", refreshIfVisible);
  refreshIfVisible(); // 挂载时立即刷新
});
onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
  window.removeEventListener("focus", refreshIfVisible);
  document.removeEventListener("visibilitychange", refreshIfVisible);
});

// 文件变更摘要文本
function getChangeSummary(turn) {
  const count = turn.filePaths?.length || 0;
  return count > 0 ? `${count} ${"个文件"}` : "";
}
</script>

<template>
  <Card>
    <div class="flex h-full flex-col gap-3">
      <!-- Header -->
      <div class="flex items-center justify-between gap-2">
        <h2 class="text-sm font-medium text-white">
          {{ "版本历史" }}
        </h2>
        <div class="flex items-center gap-2">
          <input
            v-model="searchQuery"
            type="text"
            :placeholder="'搜索...'"
            class="w-44 rounded-[5px] border border-[#3a3a3a] bg-[#1f1f1f] px-3 py-1.5 text-sm text-[#e5e5e5] placeholder-[#555] outline-none transition-colors focus:border-[#10AD5D]"
          />
          <button
            class="cursor-pointer text-lg text-[#888] hover:text-[#e5e5e5] px-1 transition-colors"
            :disabled="isLoading"
            @click="handleRefresh"
          ><span class="icon-[fa--rotate-right]"></span></button>
        </div>
      </div>

      <!-- Loading / Error / Empty -->
      <div v-if="isLoading" class="flex items-center justify-center py-6">
        <span class="text-xs text-[#686868]">{{ "加载中..." }}</span>
      </div>
      <div v-else-if="hasError" class="rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-2 py-1.5 text-xs text-[#fca5a5]">
        {{ appState.recentTurnsError }}
      </div>
      <div v-else-if="isEmpty" class="flex flex-col items-center justify-center py-8">
        <span class="text-xs text-[#686868]">{{ "暂无版本历史" }}</span>
      </div>

      <!-- Project tree: 项目 → 对话 → 轮次 -->
      <div v-else class="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto" style="scrollbar-width: thin;">
        <div v-for="project in projectNames" :key="project" class="flex flex-col gap-0.5">
          <!-- Project header -->
          <div class="flex cursor-pointer items-center gap-1.5 rounded-[4px] px-2 py-1 transition-colors hover:bg-[#1f1f1f] select-none"
            @click="toggleProject(project)">
            <span class="text-[9px] text-[#555] transition-transform" :class="isProjectExpanded(project) ? 'rotate-90' : ''">▶</span>
            <span class="text-sm text-[#aaa]">{{ project }}</span>
            <span class="text-sm text-[#555]">{{ projectTree[project].length }}</span>
            <button v-if="projectEventList(projectTree[project][0]?.projectRoot).length"
              class="ml-1 shrink-0 rounded bg-[#fbbf24]/20 px-1.5 py-0.5 text-[10px] text-[#fbbf24] cursor-pointer hover:bg-[#fbbf24]/30"
              :title="'协调事件'"
              @click.stop="toggleProjectEvents(projectTree[project][0]?.projectRoot)">
              <span class="icon-[mdi--bell]"></span> {{ projectEventList(projectTree[project][0]?.projectRoot).length }}
            </button>
          </div>

          <!-- Project events panel -->
          <template v-if="isProjectExpanded(project) && expandedEvents[projectTree[project][0]?.projectRoot]">
            <div class="ml-4 mb-1 flex flex-col gap-0.5 rounded-[5px] border border-[#2a2a2a] bg-[#161616] p-2">
              <div v-if="!projectEventList(projectTree[project][0]?.projectRoot).length" class="text-[11px] text-[#555]">
                {{ "暂无协调事件" }}
              </div>
              <div v-for="ev in projectEventList(projectTree[project][0]?.projectRoot).slice(0, 20)" :key="ev.seq"
                class="flex items-start gap-1.5 rounded-[3px] px-1.5 py-1">
                <span class="mt-0.5 shrink-0 rounded px-1.5 py-0.5 text-[10px]" :class="eventLabel(ev.type).cls">
                  {{ eventLabel(ev.type).zh }}
                </span>
                <span class="min-w-0 flex-1 text-[11px] text-[#ccc]">
                  <span v-if="ev.filePath" class="block truncate text-[10px] text-[#888]" :title="ev.filePath">{{ ev.filePath.split(/[/\\]/).pop() }}</span>
                  <span v-if="ev.message" class="block truncate text-[10px] text-[#666]">{{ ev.message }}</span>
                </span>
                <span class="shrink-0 text-[10px] text-[#555]">{{ formatEventTime(ev.createdAt) }}</span>
              </div>
            </div>
          </template>

          <template v-if="isProjectExpanded(project)">
            <div v-for="conv in projectTree[project]" :key="conv.convId" class="flex flex-col gap-0.5">
              <!-- Conversation header -->
              <div class="ml-2 flex cursor-pointer items-center gap-1.5 rounded-[4px] px-2 py-1 transition-colors hover:bg-[#1a1a1a] select-none"
                @click="toggleConversation(project, conv.convId)">
                <span class="text-[8px] text-[#555] transition-transform" :class="isConversationExpanded(project, conv.convId) ? 'rotate-90' : ''">▶</span>
                <span class="max-w-[180px] truncate text-sm text-[#ccc]">{{ conv.label }}</span>
                <span v-if="conv.isActive" class="shrink-0 rounded bg-[#10AD5D] px-1.5 py-0.5 text-xs text-white">✓</span>
                <span class="ml-auto shrink-0 text-xs text-[#555]">{{ formatRelativeTime(conv.latestAt) }}</span>
              </div>

              <template v-if="isConversationExpanded(project, conv.convId)">
                <div v-for="turn in conv.turns"
                  :key="`${turn.conversationId}-${turn.turnSeq}`"
                  class="ml-6 flex items-start justify-between gap-1.5 rounded-[5px] px-2.5 py-2 transition-colors"
                  :class="turn.isActive
                    ? 'border border-[#10AD5D] bg-[#0a2e1a]'
                    : 'bg-[#1f1f1f]'">
                  <div class="flex min-w-0 flex-1 flex-col gap-1.5">
                    <div class="flex items-start gap-1.5">
                      <span class="shrink-0 whitespace-nowrap rounded-[3px] bg-[#252525] px-1.5 py-0.5 text-xs text-[#777]">{{ formatRelativeTime(turn.editedAt) }}</span>
                      <span v-if="turn.jumpedAt" class="shrink-0 whitespace-nowrap rounded-[3px] bg-[#f59e0b]/20 px-1.5 py-0.5 text-xs text-[#fbbf24]"><span class="icon-[fa--reply]"></span> {{ formatRelativeTime(turn.jumpedAt) }}</span>
                      <span class="line-clamp-2 mt-[1px] text-xs" :class="turn.isActive ? 'text-[#4ade80]' : 'text-[#ccc]'">{{ turn.userMessage }}</span>
                      <span v-if="turn.isActive" class="shrink-0 self-start rounded-[3px] bg-[#10AD5D] px-1.5 py-0.5 text-[10px] text-white"><span class="icon-[fa--check]"></span></span>
                    </div>
                    <div class="flex flex-wrap items-center gap-1">
                      <div v-if="turn.filePaths?.length" class="flex flex-wrap gap-1">
                        <span v-for="f in turn.filePaths.slice(0,3)" :key="f"
                          class="max-w-[140px] truncate rounded-[3px] px-1.5 py-0.5 text-[11px]"
                          :class="getFileStyle(turn, f)" :title="f">{{ getFileName(f) }}</span>
                        <span v-if="turn.filePaths.length > 3" class="rounded-[3px] bg-[#1f1f1f] px-1.5 py-0.5 text-[11px] text-[#555]">+{{ turn.filePaths.length-3 }}</span>
                      </div>
                      <span v-if="turn.modelName" class="rounded-[3px] bg-[#1a2a4a] px-1.5 py-0.5 text-[11px] text-[#60a5fa]">{{ turn.modelName }}</span>
                    </div>
                  </div>
                  <div class="flex shrink-0 items-center gap-1.5">
                    <Button variant="default" class="!min-w-[56px]" @click="handleViewDiff(turn)">{{ "变更" }}</Button>
                    <Button
                      variant="primary"
                      class="!min-w-[56px]"
                      :class="turn.isActive ? '!cursor-default opacity-40' : (appState.undoingTurn ? 'cursor-wait opacity-50' : '')"
                      :disabled="turn.isActive || appState.undoingTurn"
                      @click="handleJump(turn)"
                    >{{ turn.isActive ? '✓' : (appState.undoingTurn ? '...' : '跳转') }}</Button>
                  </div>
                </div>
              </template>
            </div>
          </template>
        </div>
      </div>
    </div>
    
    <FileDiffViewer
      v-if="diffVisible"
      :diff="appState.turnDiff"
      :loading="appState.turnDiffLoading"
      :error="appState.turnDiffError"
      @close="handleCloseDiff"
    />
  </Card>
</template>
