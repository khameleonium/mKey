<!--
  VirtualDevicesPage — страница «Виртуальные устройства» (FR-VD-8, ADR-0041): все устройства,
  которые mKey создаёт в системе, из всех проектов. У каждого — карточка с состоянием простыми
  словами (подключено / выключено / ошибка), переключателем, проектом, раскладкой и кнопками
  «Проверить», «Изменить раскладку», «Удалить». «Новое устройство» открывает мастер.
  Устройство живёт в проекте: включить устройство — включить его проект.
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import Toggle from "../../lib/components/Toggle.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { navigate } from "../../lib/router.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import type { InputDevice, VirtualCard, VirtualTemplateInfo } from "../../lib/types";
  import LiveTest from "./LiveTest.svelte";
  import NewDevice from "./NewDevice.svelte";
  import { KINDS, targets } from "./presets";

  /** Данные страницы: устройства, состав шаблонов, устройства ввода и их авто-ID (для мастера). */
  let cards = $state<VirtualCard[]>([]);
  let templates = $state<VirtualTemplateInfo[]>([]);
  let devices = $state<InputDevice[]>([]);
  let autoIds = $state<Record<string, string>>({});
  /** loaded — первая загрузка прошла (пока нет — без «устройств нет»); unavailable — модуль выключен. */
  let loaded = $state(false);
  let unavailable = $state(false);

  /** Открытые окна: мастер, проверка, вопрос об удалении. */
  let creating = $state(false);
  let testing = $state<{ name: string; system: string; template: string } | null>(null);
  let deleting = $state<VirtualCard | null>(null);

  /** load перечитывает устройства (и устройства ввода — для мастера). */
  async function load(): Promise<void> {
    try {
      const r = await api.virtualDevices();
      cards = r.all ?? [];
      templates = r.template_info ?? [];
      unavailable = false;
    } catch {
      unavailable = true;
    }
    loaded = true;
    try {
      const d = await api.devices();
      devices = d.devices;
      autoIds = d.auto_ids ?? {};
    } catch {
      // Без модуля ввода мастер предложит «любую клавиатуру».
    }
  }

  // Загрузка при открытии и при изменении проектов (устройства создаются чуть позже — перечитываем
  // и через полсекунды).
  $effect(() => {
    void load();
    let later: ReturnType<typeof setTimeout> | undefined;
    const off = onTopic("store.projects_changed", () => {
      void load();
      clearTimeout(later);
      later = setTimeout(() => void load(), 500);
    });
    return () => {
      off();
      clearTimeout(later);
    };
  });

  /** icon — значок вида устройства. */
  function icon(template: string): string {
    return KINDS.find((k) => k.id === template)?.icon ?? "🔌";
  }

  /** setOn включает или выключает устройство (его проект) и объясняет, что произошло. */
  async function setOn(c: VirtualCard, on: boolean): Promise<void> {
    try {
      await api.setProjectEnabled(c.project, on);
      toast(t(on ? "vd.restart_hint" : "vd.turned_off", { system: c.system_name }));
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** test открывает проверку вживую; выключенное устройство сначала включается. */
  async function test(name: string, project: string): Promise<void> {
    const c = cards.find((x) => x.name === name && x.project === project);
    if (!c || c.state !== "on") {
      try {
        await api.setProjectEnabled(project, true);
        toast(t("vd.restart_hint", { system: "mKey " + name }));
      } catch (e) {
        toast(errorText(e), "error");
        return;
      }
    }
    await load();
    const card = cards.find((x) => x.name === name && x.project === project);
    testing = { name, system: "mKey " + name, template: card?.template ?? "" };
  }

  /** removeDevice удаляет из проекта только устройство и привязки к нему. */
  async function removeDevice(c: VirtualCard): Promise<void> {
    try {
      const pf = await api.project(c.project);
      const p = pf.project;
      p.virtual_devices = (p.virtual_devices ?? []).filter((v) => v.name !== c.name);
      p.bindings = (p.bindings ?? []).filter((b) => !targets(b, c.name));
      await api.saveProject(c.project, p);
      toast(t("vd.deleted", { name: c.system_name }));
      deleting = null;
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** removeProject удаляет проект устройства целиком. */
  async function removeProject(c: VirtualCard): Promise<void> {
    try {
      await api.deleteProject(c.project);
      toast(t("vd.deleted", { name: c.project_name || c.project }));
      deleting = null;
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }
</script>

<div class="head">
  <h1>{t("vd.title")}</h1>
  <button class="primary" disabled={unavailable} onclick={() => (creating = true)}
    >＋ {t("vd.new")}</button
  >
</div>
<p class="muted intro">{t("vd.intro")}</p>

{#if unavailable}
  <div class="note warn">{t("vd.unavailable")}</div>
{:else if loaded && cards.length === 0}
  <div class="card empty">{t("vd.empty")}</div>
{/if}

<!-- Карточки устройств -->
<div class="cards">
  {#each cards as c (c.project + "/" + c.name)}
    <div class="card dev" class:off={c.state === "off"}>
      <div class="top">
        <span class="icon" aria-hidden="true">{icon(c.template)}</span>
        <span class="title">
          <b>{c.system_name}</b>
          <span class="muted">{t(`vd.kind.${c.template}`)}</span>
        </span>
        <Toggle
          checked={c.state !== "off"}
          title={t(c.state === "off" ? "vd.turn_on" : "vd.turn_off")}
          onchange={(on) => void setOn(c, on)}
        />
      </div>

      <!-- Состояние простыми словами -->
      <p class="state {c.state}">
        {#if c.state === "on"}🟢 {t("vd.state.on", { system: c.system_name })}
        {:else if c.state === "off"}⚪ {t("vd.state.off")}
        {:else}⚠ {t("vd.state.error", { error: c.error ?? "" })}{/if}
      </p>

      <!-- Проект, раскладка, что ещё включается вместе с устройством -->
      <p>
        <a href="#/editor/{encodeURIComponent(c.project)}"
          >{t("vd.project", { name: c.project_name || c.project })}</a
        >
      </p>
      <p class="muted">
        {c.bindings > 0 ? t("vd.bindings", { n: c.bindings }) : t("vd.bindings_none")}
      </p>
      {#if c.hides && c.bindings > 0}<p class="muted">{t("vd.hides")}</p>{/if}
      {#if c.others > 0}<p class="muted">{t("vd.others", { n: c.others })}</p>{/if}

      <div class="actions">
        <button onclick={() => void test(c.name, c.project)}>▶ {t("vd.test")}</button>
        <button onclick={() => navigate("editor", c.project)}>✎ {t("vd.edit")}</button>
        <button class="ghost" onclick={() => (deleting = c)}>🗑 {t("vd.delete")}</button>
      </div>
    </div>
  {/each}
</div>

{#if cards.length}<p class="muted">{t("vd.emergency")}</p>{/if}

{#if creating}
  <NewDevice
    {cards}
    {templates}
    {devices}
    {autoIds}
    onclose={() => (creating = false)}
    ontest={(name, project) => {
      creating = false;
      void test(name, project);
    }}
  />
{/if}

{#if testing}
  <LiveTest
    name={testing.name}
    system={testing.system}
    info={templates.find((x) => x.id === testing?.template)}
    onclose={() => (testing = null)}
  />
{/if}

<!-- Удаление: только устройство или весь проект -->
{#if deleting}
  {@const c = deleting}
  <Modal title={t("vd.delete_title", { name: c.system_name })} onclose={() => (deleting = null)}>
    {#if c.others > 0}
      <div class="choice">
        <button onclick={() => void removeDevice(c)}>
          <b>{t("vd.delete_only")}</b><br /><span class="muted"
            >{t("vd.delete_only_hint", { project: c.project_name || c.project })}</span
          >
        </button>
        <button class="danger-outline" onclick={() => void removeProject(c)}>
          <b>{t("vd.delete_project", { project: c.project_name || c.project })}</b><br /><span
            class="muted">{t("vd.delete_project_hint")}</span
          >
        </button>
      </div>
    {:else}
      <p>{t("vd.delete_simple", { project: c.project_name || c.project })}</p>
    {/if}
    {#snippet footer()}
      <button onclick={() => (deleting = null)}>{t("common.cancel")}</button>
      {#if c.others === 0}
        <button class="danger" onclick={() => void removeProject(c)}>{t("vd.delete")}</button>
      {/if}
    {/snippet}
  </Modal>
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    gap: 10px 16px;
    align-items: center;
    justify-content: space-between;
  }
  .intro {
    max-width: 60em;
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 320px), 1fr));
    gap: 14px;
    margin: 16px 0;
  }
  .dev p {
    margin: 6px 0;
  }
  /* Выключенное устройство — приглушено, но читаемо. */
  .dev.off .top {
    opacity: 0.75;
  }
  .top {
    display: flex;
    gap: 12px;
    align-items: center;
  }
  .icon {
    font-size: 1.8rem;
    line-height: 1;
  }
  .title {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .state {
    font-weight: 600;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 10px;
  }
  .empty {
    margin: 16px 0;
  }
  .choice {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .choice button {
    text-align: left;
    padding: 10px 12px;
  }
  .danger-outline {
    border-color: var(--danger);
  }
</style>
