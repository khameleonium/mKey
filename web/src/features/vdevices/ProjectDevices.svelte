<!--
  ProjectDevices — раздел редактора проекта «Виртуальные устройства и раскладка» (FR-VD-8):
  устройства проекта и клавиши, которые ими управляют, — вместо раскладки только в тексте файла.
  Показывается, только если в проекте есть виртуальные устройства.
  Props: devices — устройства проекта (virtual_devices); bindings — привязки проекта;
  onchange(bindings) — новые привязки (редактор отмечает проект изменённым).
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { navigate } from "../../lib/router.svelte";
  import type { Binding, VirtualDeviceSpec, VirtualTemplateInfo } from "../../lib/types";
  import DeviceLayout from "./DeviceLayout.svelte";

  let {
    devices,
    bindings,
    onchange,
  }: {
    devices: VirtualDeviceSpec[];
    bindings: Binding[];
    onchange: (bindings: Binding[]) => void;
  } = $props();

  /** templates — состав шаблонов (для «Добавить»); без модуля вывода — пусто. */
  let templates = $state<VirtualTemplateInfo[]>([]);
  $effect(() => {
    void api
      .virtualDevices()
      .then((r) => (templates = r.template_info ?? []))
      .catch(() => {});
  });
</script>

<section class="card vdevs">
  <div class="head">
    <h2>{t("vd.editor_title")}</h2>
    <button class="small" onclick={() => navigate("virtual")}>🎮 {t("vd.title")}</button>
  </div>
  <p class="muted">{t("vd.editor_hint")}</p>
  {#each devices as d (d.name)}
    <DeviceLayout
      device={d}
      {bindings}
      info={templates.find((x) => x.id === d.template)}
      {onchange}
    />
  {/each}
</section>

<style>
  .vdevs {
    margin-bottom: 16px;
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 12px;
    align-items: center;
    justify-content: space-between;
  }
  h2 {
    margin: 0;
  }
</style>
