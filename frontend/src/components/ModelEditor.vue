<script setup>
import Button from "@/components/ui/Button.vue";
import Combobox from "@/components/ui/Combobox.vue";
import Input from "@/components/ui/Input.vue";
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";
import Select from "@/components/ui/Select.vue";
import Tooltip from "@/components/ui/Tooltip.vue";
import { useMessage } from "@/composables/useMessage";
import {
  ANTHROPIC_THINKING_EFFORT_DEFAULT,
  appState,
  buildModelAdapterTestRequestHash,
  createEmptyModelAdapter,
  CUSTOM_HEADERS_DEFAULT_JSON,
  EXTRA_PARAMS_DEFAULT_JSON,
  fetchAvailableModelIDs,
  getModelAdapterTestResult,
  getModelAdapterTestResultByID,
  isModelAdapterTestResultStale,
  normalizeModelAdapter,
  OPENAI_ENDPOINT_CHAT_COMPLETIONS,
  OPENAI_ENDPOINT_CUSTOM,
  OPENAI_ENDPOINT_RESPONSES,
  OPENAI_EXTRA_PARAMS_DEFAULT_JSON,
  runModelAdapterTest,
  saveModelAdapterAt,
  toUserError,
  validateModelAdapters,
} from "@/state/appState";
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import {
  getVisionSkillStatus,
  testLocalVisionChannel,
  testRemoteVision,
} from "@/services/clientApi";

const modelTypeTabs = [
  { label: "OpenAI", value: "openai", icon: "icon-[bxl--openai]" },
  { label: "Anthropic", value: "anthropic", icon: "icon-[logos--claude-icon]" },
];

const reasoningEffortOptions = [
  { label: "不设置", value: "", icon: "icon-[mdi--minus-circle-outline]" },
  { label: "低", value: "low", icon: "icon-[mdi--head-outline]" },
  { label: "中", value: "medium", icon: "icon-[mdi--head-lightbulb-outline]" },
  { label: "高", value: "high", icon: "icon-[mdi--brain]" },
  { label: "极高", value: "xhigh", icon: "icon-[mdi--head-cog-outline]" },
  { label: "最高", value: "max", icon: "icon-[mdi--brain]" },
];

const anthropicThinkingEffortOptions = [
  { label: "低", value: "low", icon: "icon-[mdi--head-outline]" },
  { label: "中", value: "medium", icon: "icon-[mdi--head-lightbulb-outline]" },
  { label: "高", value: "high", icon: "icon-[mdi--brain]" },
  { label: "极高", value: "xhigh", icon: "icon-[mdi--head-cog-outline]" },
  { label: "Max", value: "max", icon: "icon-[mdi--brain]" },
];

const openAIEndpointOptions = [
  { label: "/v1/responses", value: OPENAI_ENDPOINT_RESPONSES, icon: "icon-[mdi--api]" },
  { label: "/v1/chat/completions", value: OPENAI_ENDPOINT_CHAT_COMPLETIONS, icon: "icon-[mdi--message-text-outline]" },
  { label: "自定义路径(请输入完整请求地址)", value: OPENAI_ENDPOINT_CUSTOM, icon: "icon-[mdi--pencil-outline]" },
];

const props = defineProps({
  index: { type: Number, default: -1 },
  adapter: { type: Object, default: () => createEmptyModelAdapter() },
});

const emit = defineEmits(["close", "saved"]);
const message = useMessage();

const editorIndex = ref(props.index);
const draft = reactive(normalizeModelAdapter(props.adapter));
if (!draft.type) {
  draft.type = "openai";
}
const lastTestAdapterID = ref("");
const localTestFailure = ref("");
const availableModelIDs = ref(draft.modelID ? [draft.modelID] : []);
const modelListLoading = ref(false);
const modelListRequestSeq = ref(0);
let modelListDebounceTimer = 0;

// ── 图片输入（vision）状态 ──────────────────────────────
const visionModels = ref([]);
const visionModelsFetching = ref(false);
const visionModelsError = ref("");
const visionSkillStatus = ref(null);
const visionSkillStatusError = ref("");
const visionSkillStatusLoading = ref(false);
const remoteVisionTesting = ref(false);
const remoteVisionTestResult = ref(null);
const localVisionTestingChannel = ref("");
const localVisionTestResults = reactive({});

const effectiveVisionProviderType = computed(() => {
  if (draft.visionUseProviderDefaults) {
    return draft.type || "openai";
  }
  return draft.visionProviderType || draft.type || "openai";
});

const effectiveVisionBaseURL = computed(() => {
  if (draft.visionUseProviderDefaults) {
    return draft.baseURL || "";
  }
  return draft.visionBaseURL || draft.baseURL || "";
});

const effectiveVisionAPIKey = computed(() => {
  if (draft.visionUseProviderDefaults) {
    return draft.apiKey || "";
  }
  return draft.visionAPIKey || draft.apiKey || "";
});

const visionModelOptions = computed(() =>
  visionModels.value.map((modelID) => ({ label: modelID, value: modelID })),
);

function createOptionalPositiveIntegerModel(key) {
  return computed({
    get() {
      return draft[key] > 0 ? String(draft[key]) : "";
    },
    set(value) {
      const text = String(value || "").trim();
      draft[key] = /^\d+$/.test(text) && Number(text) > 0 ? Number(text) : 0;
    },
  });
}

const maxCompletionTokensInput = createOptionalPositiveIntegerModel("maxCompletionTokens");
const anthropicMaxTokensInput = createOptionalPositiveIntegerModel("anthropicMaxTokens");
const contextWindowTokensInput = createOptionalPositiveIntegerModel("contextWindowTokens");
const interfacePlaceholder = computed(() =>
  draft.type === "anthropic" ? "例如：https://api.anthropic.com" : "例如：https://api.openai.com/v1",
);
const modelOptions = computed(() => availableModelIDs.value.map((modelID) => ({
  label: modelID,
  value: modelID,
  icon: "icon-[mdi--cube-outline]",
})));
const canFetchModels = computed(() => Boolean(
  draft.type && String(draft.baseURL || "").trim() && String(draft.apiKey || "").trim(),
));
const selectedTestAdapter = computed(() => normalizeModelAdapter(draft));
const currentRequestHash = computed(() => buildModelAdapterTestRequestHash(selectedTestAdapter.value));
const directModelTestResult = computed(() => getModelAdapterTestResult(selectedTestAdapter.value));
const rememberedModelTestResult = computed(() =>
  lastTestAdapterID.value ? getModelAdapterTestResultByID(lastTestAdapterID.value) : null,
);
const activeModelTestResult = computed(() => directModelTestResult.value || rememberedModelTestResult.value);
const modelTestResultStale = computed(() =>
  isModelAdapterTestResultStale(selectedTestAdapter.value, activeModelTestResult.value),
);
const isCurrentConfigTesting = computed(() => directModelTestResult.value?.status === "running");
const modelTestSummary = computed(() => {
  if (localTestFailure.value) {
    return localTestFailure.value;
  }
  return activeModelTestResult.value?.summaryText || "尚未测试";
});

function ensureOpenAIExtraParamsJSON() {
  if (!String(draft.openAIExtraParamsJSON || "").trim()) {
    draft.openAIExtraParamsJSON = OPENAI_EXTRA_PARAMS_DEFAULT_JSON;
  }
}

function ensureCustomHeadersJSON() {
  if (!String(draft.customHeadersJSON || "").trim()) {
    draft.customHeadersJSON = CUSTOM_HEADERS_DEFAULT_JSON;
  }
}

function ensureAnthropicExtraParamsJSON() {
  if (!String(draft.anthropicExtraParamsJSON || "").trim()) {
    draft.anthropicExtraParamsJSON = EXTRA_PARAMS_DEFAULT_JSON;
  }
}

function ensureAnthropicThinkingEffort() {
  if (!String(draft.anthropicThinkingEffort || "").trim()) {
    draft.anthropicThinkingEffort = ANTHROPIC_THINKING_EFFORT_DEFAULT;
  }
}

const fieldTips = {
  displayName: "仅用于界面展示，便于你区分不同模型。",
  modelID: "可以直接输入模型标识，或从服务端返回的列表中选择。",
  baseURL: "模型服务的 API 根地址，通常为兼容 OpenAI 或 Anthropic 的接口入口。",
  apiKey: "调用该模型服务需要使用的访问密钥。",
  contextWindowTokens: "模型单次可接受的最大上下文 Token 数。留空时使用默认值。",
  reasoningEffort: "仅当模型支持 reasoning_effort 时才选择推理强度；选择“不设置”后，请求不会携带该参数。越高通常越稳，但也可能更慢。",
  maxCompletionTokens: "单次回复允许生成的最大 Token 数。留空时使用默认值。",
  openAIEndpoint: "选择接口协议端点。选“自定义路径”时，请在接口地址栏填写完整请求地址（含 /chat/completions 或 /responses 路径后缀），系统会根据末段自动判断协议形态。",
  openAIExtraParams: "开启后会把 JSON 对象覆盖到 OpenAI 请求体。同名字段以这里为准。OpenAI service_tier 支持 auto、default、flex、scale、priority。",
  customHeaders: "开启后会把 JSON 对象覆盖到最终请求头。同名请求头以这里为准，值必须是字符串。",
  anthropicExtraParams: "开启后会把 JSON 对象覆盖到 Anthropic 请求体。同名字段以这里为准。",
  anthropicMaxTokens: "Anthropic 模型单次回复允许生成的最大 Token 数。留空时使用默认值。",
  anthropicThinkingEffort: "Anthropic adaptive thinking 的思考强度。请求会固定使用新版 thinking.type=adaptive。",
  tooltipData: "模型列表 hover 时显示的备注说明。",
  visionEnabled: "开启后，粘贴/拖拽的图片会转成文字摘要再发给模型。处理方式可选本机 ds-vision-skill（GLM/OCR）或远程视觉模型；原图会保存到 D:\\ds-vision-data\\pasted\\，路径会随摘要一起提供给模型。关闭则图片原样转发（纯文本中转可能报错）。",
  visionMode: "图片输入的处理方式：本机识别使用本机 ds-vision-skill（GLM/OCR）；远程视觉模型使用你配置的模型服务商识别图片，识别失败会自动回退到本机识别。",
  visionUseProviderDefaults: "开启后直接复用上方模型的提供商/接口地址/访问密钥，只需选择视觉模型；关闭后可自定义覆盖，方便使用便宜或免费的视觉模型。",
  visionProviderType: "远程视觉模型的服务提供商类型。",
  visionModelID: "用于识别图片内容的视觉模型标识（例如 glm-4v-flash、qwen-vl-plus）。可通过“拉取模型”获取服务商支持的模型列表，也可手动填写。",
  visionBaseURL: "远程视觉模型服务的 API 根地址，留空时使用上方模型的接口地址。",
  visionAPIKey: "远程视觉模型服务的访问密钥，留空时使用上方模型的访问密钥。",
};

async function refreshModelList() {
  const baseURL = String(draft.baseURL || "").trim();
  const apiKey = String(draft.apiKey || "").trim();
  if (!baseURL || !apiKey || !draft.type) {
    modelListRequestSeq.value += 1;
    availableModelIDs.value = [];
    modelListLoading.value = false;
    return [];
  }

  const requestSeq = modelListRequestSeq.value + 1;
  modelListRequestSeq.value = requestSeq;
  modelListLoading.value = true;
  availableModelIDs.value = [];
  try {
    const models = await fetchAvailableModelIDs({
      type: draft.type,
      baseURL,
      apiKey,
      customHeadersEnabled: draft.customHeadersEnabled,
      customHeadersJSON: draft.customHeadersJSON,
    });
    if (requestSeq !== modelListRequestSeq.value) {
      return availableModelIDs.value;
    }
    availableModelIDs.value = models;
    return models;
  } catch (_error) {
    if (requestSeq === modelListRequestSeq.value) {
      availableModelIDs.value = [];
    }
    return availableModelIDs.value;
  } finally {
    if (requestSeq === modelListRequestSeq.value) {
      modelListLoading.value = false;
    }
  }
}

async function persistDraft() {
  const adapter = normalizeModelAdapter(draft);

  const singleCheck = validateModelAdapters([adapter]);
  if (singleCheck) {
    message(singleCheck);
    return { ok: false, error: singleCheck, adapter: null };
  }

  const result = await saveModelAdapterAt(editorIndex.value, adapter);
  if (!result.ok) {
    message(result.error);
    return { ok: false, error: result.error, adapter: null };
  }

  if (typeof result.index === "number") {
    editorIndex.value = result.index;
  }
  if (result.adapter) {
    Object.assign(draft, normalizeModelAdapter(result.adapter));
  } else {
    Object.assign(draft, adapter);
  }
  return {
    ok: true,
    error: "",
    adapter: result.adapter ? normalizeModelAdapter(result.adapter) : normalizeModelAdapter(adapter),
  };
}

async function handleSave() {
  const result = await persistDraft();
  if (!result.ok) {
    return;
  }
  emit("saved", result.adapter);
  emit("close");
}

function handleCancel() {
  emit("close");
}

function handleModelTypeChange(type) {
  draft.type = type;
  modelListRequestSeq.value += 1;
  modelListLoading.value = false;
  availableModelIDs.value = [];
  draft.modelID = "";
  if (type === "openai" && !draft.openAIEndpoint) {
    draft.openAIEndpoint = OPENAI_ENDPOINT_RESPONSES;
  } else if (type === "anthropic") {
    ensureAnthropicThinkingEffort();
  }
}

async function loadVisionSkillStatus() {
  visionSkillStatusLoading.value = true;
  visionSkillStatusError.value = "";
  try {
    visionSkillStatus.value = await getVisionSkillStatus();
  } catch (error) {
    visionSkillStatusError.value = toUserError(error);
  } finally {
    visionSkillStatusLoading.value = false;
  }
}

async function handleTestRemoteVision() {
  if (!draft.visionModelID) {
    remoteVisionTestResult.value = { ok: false, summaryText: "请先填写视觉模型标识", error: "请先填写视觉模型标识" };
    return;
  }
  remoteVisionTesting.value = true;
  remoteVisionTestResult.value = null;
  try {
    remoteVisionTestResult.value = await testRemoteVision({
      providerType: effectiveVisionProviderType.value,
      baseURL: effectiveVisionBaseURL.value,
      apiKey: effectiveVisionAPIKey.value,
      modelID: draft.visionModelID,
    });
  } catch (error) {
    remoteVisionTestResult.value = { ok: false, summaryText: toUserError(error), error: toUserError(error) };
  } finally {
    remoteVisionTesting.value = false;
  }
}

async function handleTestLocalVisionChannel(channel) {
  localVisionTestingChannel.value = channel;
  try {
    localVisionTestResults[channel] = await testLocalVisionChannel({ channel });
  } catch (error) {
    localVisionTestResults[channel] = { ok: false, summaryText: toUserError(error), error: toUserError(error) };
  } finally {
    localVisionTestingChannel.value = "";
  }
}

async function handleFetchVisionModels() {
  const baseURL = effectiveVisionBaseURL.value;
  const apiKey = effectiveVisionAPIKey.value;
  if (!baseURL || !apiKey) {
    visionModelsError.value = "请先填写接口地址与访问密钥，或开启“跟随上方模型配置”";
    return;
  }
  visionModelsFetching.value = true;
  visionModelsError.value = "";
  visionModels.value = [];
  try {
    const models = await fetchAvailableModelIDs({
      type: effectiveVisionProviderType.value,
      baseURL,
      apiKey,
    });
    visionModels.value = Array.isArray(models) ? models : [];
    if (visionModels.value.length === 0) {
      visionModelsError.value = "未拉取到任何模型";
    }
  } catch (error) {
    visionModelsError.value = toUserError(error);
  } finally {
    visionModelsFetching.value = false;
  }
}

async function handleTest() {
  localTestFailure.value = "";
  try {
    const saved = await persistDraft();
    if (!saved.ok || !saved.adapter) {
      return;
    }
    const result = await runModelAdapterTest(saved.adapter);
    if (result?.adapterID) {
      lastTestAdapterID.value = result.adapterID;
    }
  } catch (error) {
    const latest = getModelAdapterTestResult(draft);
    if (latest?.adapterID) {
      lastTestAdapterID.value = latest.adapterID;
      return;
    }
    localTestFailure.value = toUserError(error);
  }
}

watch(
  directModelTestResult,
  (result) => {
    if (!result?.adapterID) {
      return;
    }
    lastTestAdapterID.value = result.adapterID;
    if (result.status !== "running") {
      localTestFailure.value = "";
    }
  },
  { immediate: true },
);

watch(currentRequestHash, () => {
  localTestFailure.value = "";
});

watch(
  () => draft.openAIExtraParamsEnabled,
  (enabled) => {
    if (enabled) {
      ensureOpenAIExtraParamsJSON();
    }
  },
);

watch(
  () => draft.customHeadersEnabled,
  (enabled) => {
    if (enabled) {
      ensureCustomHeadersJSON();
    }
  },
);

watch(
  () => draft.anthropicExtraParamsEnabled,
  (enabled) => {
    if (enabled) {
      ensureAnthropicExtraParamsJSON();
    }
  },
);

watch(
  () => [draft.type, draft.baseURL, draft.apiKey, draft.customHeadersEnabled, draft.customHeadersJSON],
  () => {
    window.clearTimeout(modelListDebounceTimer);
    const baseURL = String(draft.baseURL || "").trim();
    const apiKey = String(draft.apiKey || "").trim();
    if (!baseURL || !apiKey) {
      modelListRequestSeq.value += 1;
      modelListLoading.value = false;
      availableModelIDs.value = [];
      return;
    }
    modelListDebounceTimer = window.setTimeout(() => {
      void refreshModelList();
    }, 600);
  },
  { immediate: true },
);

onMounted(() => {
  void loadVisionSkillStatus();
});

onBeforeUnmount(() => {
  window.clearTimeout(modelListDebounceTimer);
});
</script>

<template>
  <div class="flex h-full flex-col text-[#e5e5e5]">
     <div class="flex-shrink-0 p-4"   v-if="localTestFailure || activeModelTestResult">
       <ModelAdapterTestCard
          :result="localTestFailure ? { status: 'error', error: '测试失败', summaryText: '测试失败', rawResponse: modelTestSummary } : activeModelTestResult"
          :stale="modelTestResultStale"
          :show-metrics="true"
        />
     </div>
    <div class="flex-1 min-h-0 overflow-y-auto px-4 py-4 scroll-shadow-bottom">
      <div class="flex flex-col gap-4">
        <div class="center-row gap-2">
          <button
            v-for="tab in modelTypeTabs"
            :key="tab.value"
            type="button"
            class="center-row gap-2 rounded-[8px] border px-3 py-2 text-sm transition-colors duration-150"
            :class="draft.type === tab.value
              ? 'border-[#1ca35a] bg-[#123322] text-white'
              : 'border-[#343434] bg-[#252525] text-[#a3a3a3] hover:border-[#4a4a4a] hover:text-[#e5e5e5]'"
            @click="handleModelTypeChange(tab.value)"
          >
            <span :class="[tab.icon, 'text-[16px]']"></span>
            <span>{{ tab.label }}</span>
          </button>
        </div>

        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.baseURL" />
              <span>接口地址</span>
            </span>
            <input
              v-model="draft.baseURL"
              type="text"
              :placeholder="interfacePlaceholder"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.apiKey" />
              <span>访问密钥</span>
            </span>
            <Input
              v-model="draft.apiKey"
              type="password"
              allow-visibility-toggle
              placeholder="例如：sk-xxxxxx"
              autocomplete="off"
            />
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.displayName" />
              <span>显示名称</span>
            </span>
            <input
              v-model="draft.displayName"
              type="text"
              placeholder="例如：GPT-5"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <div class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.modelID" />
              <span>模型标识</span>
            </span>
            <Combobox
              v-model="draft.modelID"
              :options="modelOptions"
              :loading="modelListLoading"
              placeholder="例如：gpt-4.1"
              empty-text="没有匹配的模型"
              aria-label="选择模型"
            >
              <template #append>
                <button
                  type="button"
                  class="center-row h-9  shrink-0 gap-1.5 whitespace-nowrap rounded-[6px] border border-[#3f3f3f] bg-[#292929] px-[8px]  text-sm text-[#d4d4d4] outline-none transition-colors hover:border-[#505050] hover:bg-[#303030] hover:text-white focus-visible:border-[#10AD5D] disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="modelListLoading || !canFetchModels"
                  @click="refreshModelList"
                >
                  <span>获取模型</span>
                </button>
              </template>
            </Combobox>
          </div>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.contextWindowTokens" />
              <span>上下文窗口</span>
            </span>
            <input
              v-model="contextWindowTokensInput"
              type="text"
              inputmode="numeric"
              placeholder="例如：200000（留空用默认值）"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label v-if="draft.type === 'openai'" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.reasoningEffort" />
              <span>推理强度</span>
            </span>
            <Select
              v-model="draft.reasoningEffort"
              :options="reasoningEffortOptions"
            />
          </label>

          <label v-if="draft.type === 'anthropic'" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.anthropicMaxTokens" />
              <span>最大输出 Token</span>
            </span>
            <input
              v-model="anthropicMaxTokensInput"
              type="text"
              inputmode="numeric"
              placeholder="例如：65536（留空用默认值）"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label v-if="draft.type === 'anthropic'" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.anthropicThinkingEffort" />
              <span>思考强度</span>
            </span>
            <Select
              v-model="draft.anthropicThinkingEffort"
              :options="anthropicThinkingEffortOptions"
            />
          </label>

        </div>

        <div v-if="draft.type === 'openai'" class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.maxCompletionTokens" />
              <span>最大输出 Token</span>
            </span>
            <input
              v-model="maxCompletionTokensInput"
              type="text"
              inputmode="numeric"
              placeholder="例如：65536（留空用默认值）"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.openAIEndpoint" />
              <span>接口端点</span>
            </span>
            <Select
              v-model="draft.openAIEndpoint"
              :options="openAIEndpointOptions"
            />
          </label>
        </div>

        <div v-if="draft.type === 'openai'" class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="flex items-center justify-between gap-3">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.openAIExtraParams" />
              <span>额外参数 JSON</span>
            </span>
            <label class="center-row gap-2 text-xs text-[#d4d4d4]">
              <input
                v-model="draft.openAIExtraParamsEnabled"
                type="checkbox"
                class="size-4 accent-[#10AD5D]"
              />
              <span>启用</span>
            </label>
          </div>
          <textarea
            v-if="draft.openAIExtraParamsEnabled"
            v-model="draft.openAIExtraParamsJSON"
            rows="5"
            spellcheck="false"
            class="mt-3 min-h-[120px] w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 py-2 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </div>

        <div v-if="draft.type === 'anthropic'" class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="flex items-center justify-between gap-3">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.anthropicExtraParams" />
              <span>Anthropic 额外参数 JSON</span>
            </span>
            <label class="center-row gap-2 text-xs text-[#d4d4d4]">
              <input
                v-model="draft.anthropicExtraParamsEnabled"
                type="checkbox"
                class="size-4 accent-[#10AD5D]"
              />
              <span>启用</span>
            </label>
          </div>
          <textarea
            v-if="draft.anthropicExtraParamsEnabled"
            v-model="draft.anthropicExtraParamsJSON"
            rows="5"
            spellcheck="false"
            class="mt-3 min-h-[120px] w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 py-2 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </div>

        <div class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="flex items-center justify-between gap-3">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.customHeaders" />
              <span>自定义请求头 JSON</span>
            </span>
            <label class="center-row gap-2 text-xs text-[#d4d4d4]">
              <input
                v-model="draft.customHeadersEnabled"
                type="checkbox"
                class="size-4 accent-[#10AD5D]"
              />
              <span>启用</span>
            </label>
          </div>
          <textarea
            v-if="draft.customHeadersEnabled"
            v-model="draft.customHeadersJSON"
            rows="5"
            spellcheck="false"
            class="mt-3 min-h-[120px] w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 py-2 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </div>

        <div class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="flex items-center justify-between gap-3">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.visionEnabled" />
              <span>图片输入支持</span>
            </span>
            <label class="center-row gap-2 text-xs text-[#d4d4d4]">
              <input
                v-model="draft.visionEnabled"
                type="checkbox"
                class="size-4 accent-[#10AD5D]"
              />
              <span>启用</span>
            </label>
          </div>

          <div v-if="draft.visionEnabled" class="mt-3 flex flex-col gap-3">
            <div class="flex items-center justify-between gap-3">
              <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                <Tooltip :content="fieldTips.visionMode" />
                <span>处理方式</span>
              </span>
              <div class="center-row gap-2">
                <button
                  type="button"
                  class="rounded-[6px] border px-3 py-1.5 text-xs transition-colors duration-150"
                  :class="draft.visionMode !== 'remote'
                    ? 'border-[#1ca35a] bg-[#123322] text-white'
                    : 'border-[#343434] bg-[#1f1f1f] text-[#a3a3a3] hover:border-[#4a4a4a] hover:text-[#e5e5e5]'"
                  @click="draft.visionMode = 'local'"
                >
                  本机识别
                </button>
                <button
                  type="button"
                  class="rounded-[6px] border px-3 py-1.5 text-xs transition-colors duration-150"
                  :class="draft.visionMode === 'remote'
                    ? 'border-[#1ca35a] bg-[#123322] text-white'
                    : 'border-[#343434] bg-[#1f1f1f] text-[#a3a3a3] hover:border-[#4a4a4a] hover:text-[#e5e5e5]'"
                  @click="draft.visionMode = 'remote'"
                >
                  远程视觉模型
                </button>
              </div>
            </div>

            <div v-if="draft.visionMode === 'remote'" class="flex flex-col gap-3 rounded-[6px] border border-[#343434] bg-[#1f1f1f] p-3">
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs text-[#a3a3a3]">远程视觉模型配置</span>
                <label class="center-row gap-2 text-xs text-[#d4d4d4]">
                  <input
                    v-model="draft.visionUseProviderDefaults"
                    type="checkbox"
                    class="size-4 accent-[#10AD5D]"
                  />
                  <Tooltip :content="fieldTips.visionUseProviderDefaults" />
                  <span>跟随上方模型配置</span>
                </label>
              </div>

              <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
                <label v-if="!draft.visionUseProviderDefaults" class="flex flex-col gap-1">
                  <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                    <Tooltip :content="fieldTips.visionProviderType" />
                    <span>模型服务提供商</span>
                  </span>
                  <Select
                    v-model="draft.visionProviderType"
                    :options="modelTypeTabs"
                  />
                </label>

                <label class="flex flex-col gap-1">
                  <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                    <Tooltip :content="fieldTips.visionModelID" />
                    <span>视觉模型</span>
                  </span>
                  <div class="center-row gap-2">
                    <input
                      v-model="draft.visionModelID"
                      type="text"
                      placeholder="例如：glm-4v-flash（可手动填写）"
                      class="h-9 min-w-0 flex-1 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
                    />
                    <Button variant="default" :disabled="visionModelsFetching" @click="handleFetchVisionModels">
                      {{ visionModelsFetching ? "拉取中..." : "拉取模型" }}
                    </Button>
                  </div>
                </label>

                <label v-if="!draft.visionUseProviderDefaults" class="flex flex-col gap-1">
                  <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                    <Tooltip :content="fieldTips.visionBaseURL" />
                    <span>接口地址</span>
                  </span>
                  <input
                    v-model="draft.visionBaseURL"
                    type="text"
                    :placeholder="draft.visionProviderType === 'anthropic' ? '例如：https://api.anthropic.com（留空跟随上方）' : '例如：https://api.openai.com/v1（留空跟随上方）'"
                    class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
                  />
                </label>

                <label v-if="!draft.visionUseProviderDefaults" class="flex flex-col gap-1">
                  <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                    <Tooltip :content="fieldTips.visionAPIKey" />
                    <span>访问密钥</span>
                  </span>
                  <Input
                    v-model="draft.visionAPIKey"
                    type="password"
                    allow-visibility-toggle
                    placeholder="留空跟随上方"
                    autocomplete="off"
                  />
                </label>
              </div>

              <div v-if="visionModelOptions.length > 0" class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">已拉取模型（选择后填入上方“视觉模型”）</span>
                <Select
                  :model-value="visionModels.includes(draft.visionModelID) ? draft.visionModelID : ''"
                  :options="visionModelOptions"
                  placeholder="请选择视觉模型"
                  @update:model-value="(value) => { if (value) { draft.visionModelID = value; } }"
                />
              </div>
              <p v-if="visionModelsError" class="text-xs text-[#fca5a5]">{{ visionModelsError }}</p>
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs text-[#a3a3a3]">测试视觉模型</span>
                <Button variant="default" :disabled="remoteVisionTesting || !draft.visionModelID" @click="handleTestRemoteVision">
                  {{ remoteVisionTesting ? "测试中..." : "测试" }}
                </Button>
              </div>
              <div
                v-if="remoteVisionTestResult"
                class="rounded-[6px] border px-3 py-2 text-xs"
                :class="remoteVisionTestResult.ok
                  ? 'border-[#1ca35a]/40 bg-[#123322] text-[#4ade80]'
                  : 'border-[#4b1d1d] bg-[#2a1313] text-[#fca5a5]'"
              >
                {{ remoteVisionTestResult.summaryText }}
              </div>
              <p v-if="draft.visionUseProviderDefaults" class="text-xs text-[#8a8a8a]">
                将使用上方模型的提供商（{{ draft.type }}）、接口地址（{{ draft.baseURL }}）与访问密钥。
              </p>
            </div>

            <div v-else class="flex flex-col gap-2 rounded-[6px] border border-[#343434] bg-[#1f1f1f] p-3">
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs text-[#a3a3a3]">本机识别通道（ds-vision-skill）</span>
                <button
                  type="button"
                  class="rounded-[6px] border border-[#343434] px-2 py-1 text-xs text-[#a3a3a3] transition-colors duration-150 hover:border-[#4a4a4a] hover:text-[#e5e5e5]"
                  :disabled="visionSkillStatusLoading"
                  @click="loadVisionSkillStatus"
                >
                  {{ visionSkillStatusLoading ? "刷新中..." : "刷新" }}
                </button>
              </div>
              <p v-if="visionSkillStatusError" class="text-xs text-[#fca5a5]">{{ visionSkillStatusError }}</p>
              <template v-else-if="visionSkillStatus?.channels?.length">
                <div
                  v-for="channel in visionSkillStatus.channels"
                  :key="channel.channel"
                  class="flex flex-col gap-1 rounded-[6px] border border-[#343434] bg-[#252525] px-3 py-2"
                >
                  <div class="flex items-center justify-between gap-3">
                    <span class="text-sm text-[#e5e5e5]">{{ channel.name }}</span>
                    <div class="center-row gap-2">
                      <span
                        class="shrink-0 rounded-[4px] px-2 py-0.5 text-xs"
                        :class="channel.configured
                          ? 'bg-[#123322] text-[#4ade80]'
                          : 'bg-[#332912] text-[#fbbf24]'"
                      >
                        {{ channel.configured ? "已配置" : "未配置" }}
                      </span>
                      <button
                        type="button"
                        class="shrink-0 rounded-[4px] border border-[#343434] px-2 py-0.5 text-xs text-[#a3a3a3] transition-colors duration-150 hover:border-[#4a4a4a] hover:text-[#e5e5e5] disabled:cursor-not-allowed disabled:opacity-60"
                        :disabled="localVisionTestingChannel !== ''"
                        @click="handleTestLocalVisionChannel(channel.channel)"
                      >
                        {{ localVisionTestingChannel === channel.channel ? "测试中..." : "测试" }}
                      </button>
                    </div>
                  </div>
                  <div class="grid grid-cols-1 gap-x-4 gap-y-0.5 text-xs text-[#a3a3a3] md:grid-cols-2">
                    <span>模型服务提供商：{{ channel.provider }}</span>
                    <span>模型：{{ channel.model }}</span>
                    <span class="truncate" :title="channel.baseURL">BaseURL：{{ channel.baseURL || "（未设置）" }}</span>
                    <span>APIKey：{{ channel.apiKeySet ? "已设置" : "未设置" }}</span>
                  </div>
                  <div
                    v-if="localVisionTestResults[channel.channel]"
                    class="text-xs"
                    :class="localVisionTestResults[channel.channel].ok ? 'text-[#4ade80]' : 'text-[#fca5a5]'"
                  >
                    {{ localVisionTestResults[channel.channel].summaryText }}
                  </div>
                </div>
                <p class="text-xs text-[#8a8a8a]">
                  Skill 目录：{{ visionSkillStatus.skillDir }}；数据目录：{{ visionSkillStatus.dataDir }}
                </p>
                <p class="text-xs text-[#8a8a8a]">
                  本机识别的模型/密钥通过 ds-vision-skill 配置（setup.ps1 -Status 查看，-SetKey/-SetCustom 设置）。
                </p>
              </template>
            </div>
          </div>
        </div>

        <label class="flex flex-col gap-1">
          <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
            <Tooltip :content="fieldTips.tooltipData" />
            <span>备注</span>
          </span>
          <textarea
            v-model="draft.tooltipData"
            rows="3"
            placeholder="例如：用于日常代码补全与问答"
            class="min-h-[96px] resize-none rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 py-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </label>

      </div>
    </div>
    <div class="flex shrink-0 items-center justify-end gap-2 px-4 py-3">
      <Button variant="default" :disabled="appState.configSaving" @click="handleCancel">取消</Button>
      <Button variant="default" :disabled="isCurrentConfigTesting || appState.configSaving" @click="handleTest">
        {{ isCurrentConfigTesting ? "测试中..." : "保存并测试" }}
      </Button>
      <Button variant="primary" :disabled="appState.configSaving" @click="handleSave">
        {{ appState.configSaving ? "保存中..." : "保存" }}
      </Button>
    </div>
  </div>
</template>
