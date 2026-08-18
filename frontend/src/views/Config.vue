<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import LocaleSelect from "@/components/LocaleSelect.vue";
import Switch from "@/components/ui/Switch.vue";
import { showModal } from "@/composables/useModal";
import { useMessage } from "@/composables/useMessage";
import {
  appState,
  openModelConfigWindow,
  persistUserConfig,
  reloadUserConfig,
  saveAutoUpdateSetting,
  saveDisableAds,
  toUserError,
} from "@/state/appState";
import { onMounted } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const message = useMessage();

async function showActionError(title, error) {
  await showModal({
    title,
    content: String(error || "服务错误").trim() || "服务错误",
  });
}

async function handleSaveConfig() {
  const result = await persistUserConfig();
  if (!result.ok) {
    await showActionError("保存失败", result.error);
    return;
  }
  await showModal({
    title: "提示",
    content: "本地配置已保存",
  });
}

async function handleOpenModelConfig() {
  try {
    await openModelConfigWindow();
  } catch (error) {
    await showActionError("打开失败", toUserError(error));
  }
}

async function handleAutoUpdateChange(disabled) {
  const result = await saveAutoUpdateSetting(disabled);
  if (!result.ok) {
    await showActionError("保存失败", result.error);
  } else {
    message.success(disabled ? "已关闭自动更新" : "已开启自动更新");
  }
}

async function handleAdsChange(disabled) {
  const result = await saveDisableAds(disabled);
  if (!result.ok) {
    await showActionError("保存失败", result.error);
  } else {
    message.success(disabled ? "已关闭广告" : "已开启广告");
  }
}

onMounted(async () => {
  await reloadUserConfig().catch(() => {});
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-4 overflow-y-auto p-4 pt-0 text-[#e5e5e5]">
    <!-- Back button -->
    <div class="flex items-center gap-3 pt-4">
      <button
        class="flex items-center gap-1 rounded-[5px] px-3 py-1.5 text-xs text-[#888] transition-colors hover:bg-[#252525] hover:text-[#e5e5e5] cursor-pointer"
        @click="router.push('/')"
      >
        <span class="text-sm">←</span>
        <span>{{ "返回" }}</span>
      </button>
    </div>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">本地配置</h2>
          <div class="text-sm text-[#a3a3a3]">
            可配置模型渠道；运行日志位于 <code>~/.cursor-local-assistant-v2/logs/</code>
          </div>
        </div>
        <Button variant="primary" :disabled="appState.configSaving" @click="handleSaveConfig">
          {{ appState.configSaving ? "保存中..." : "保存配置" }}
        </Button>
      </div>
    </Card>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">界面语言</h2>
          <div class="text-sm text-[#a3a3a3]">
            切换当前界面显示语言，设置会立即生效并保存在本机
          </div>
        </div>
        <LocaleSelect wrapper-class="w-[220px] max-w-full" />
      </div>
    </Card>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">自动更新</h2>
          <div class="text-sm text-[#a3a3a3]">
            关闭后不会自动检查更新和下载新版本，仍可通过系统托盘菜单手动检查
          </div>
        </div>
        <Switch
          :enabled="appState.disableAutoUpdate"
          :busy="appState.configSaving"
          :disabled="appState.configSaving"
          enabled-text="已关闭"
          disabled-text="已开启"
          @change="handleAutoUpdateChange"
        />
      </div>
    </Card>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">广告展示</h2>
          <div class="text-sm text-[#a3a3a3]">
            关闭后首页不展示广告区域
          </div>
        </div>
        <Switch
          :enabled="appState.disableAds"
          :busy="appState.configSaving"
          :disabled="appState.configSaving"
          enabled-text="已关闭"
          disabled-text="已开启"
          @change="handleAdsChange"
        />
      </div>
    </Card>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">模型配置</h2>
          <div class="text-sm text-[#a3a3a3]">
            已配置 {{ appState.modelAdapters.length }} 个模型适配器
          </div>
        </div>
        <Button variant="primary" @click="handleOpenModelConfig">打开模型配置</Button>
      </div>
    </Card>
  </div>
</template>
