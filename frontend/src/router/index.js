import { createRouter, createWebHashHistory } from "vue-router";
import Home from "@/views/Home.vue";
import Config from "@/views/Config.vue";
import ModelConfig from "@/views/ModelConfig.vue";

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: "/",
      component: Home,
      meta: { showIcon: true, title: "CursorUltra｜永久免费｜自定义API", directlyClose: false },
    },
    {
      path: "/config",
      component: Config,
      meta: { showIcon: false, title: "系统设置", directlyClose: true },
    },
    {
      path: "/model-config",
      component: ModelConfig,
      meta: { showIcon: false, title: "模型配置", directlyClose: true },
    },
  ],
});

export default router;
