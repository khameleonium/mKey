<!--
  GamepadWizard — мастер «второй геймпад» (FR-VD-4): из клавиатуры (и, по желанию, мыши)
  делается виртуальный геймпад. Человек выбирает устройство и вид геймпада, видит раскладку
  по умолчанию (WASD — левый стик, стрелки — правый, Пробел — нижняя кнопка…) и может поменять
  любую клавишу кнопкой «Нажмите клавишу…». mKey создаёт обычный проект (выключенный) с разделами
  virtual_devices и bindings — его потом можно править в редакторе или текстовом файле.
  Props: devices — устройства ввода; autoIds — авто-ID устройств по пути; templates — состав
  шаблонов (кнопки и оси); onclose() — закрыть.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import KeyCapture from "../../lib/components/KeyCapture.svelte";
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { navigate } from "../../lib/router.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { InputDevice, VirtualTemplateInfo } from "../../lib/types";
  import { buildProject, defaultRows, isRightStick, type Row } from "./gamepad";

  let {
    devices,
    autoIds,
    templates,
    onclose,
  }: {
    devices: InputDevice[];
    autoIds: Record<string, string>;
    templates: VirtualTemplateInfo[];
    onclose: () => void;
  } = $props();

  /** GAMEPADS — шаблоны-геймпады, которые предлагает мастер (у остальных нет стиков). */
  const GAMEPADS = ["xbox360", "ds4"];

  /** Выбор человека: шаблон, устройство-источник, имена, раскладка и настройки. */
  let template = $state("xbox360");
  let device = $state("");
  let name = $state(t("gpw.default_name"));
  let pad = $state("pad2");
  let mouseRight = $state(false);
  let hide = $state(true);
  let smooth = $state(false);
  /** busy — идёт создание; created — ID созданного проекта (показываем последний шаг). */
  let busy = $state(false);
  let created = $state("");

  /** info — состав выбранного шаблона. */
  let info = $derived(templates.find((x) => x.id === template));

  // Раскладка по умолчанию — при открытии и смене шаблона (правки человека — поверх неё).
  let rows = $derived<Row[]>(info ? defaultRows(info.buttons, info.axes) : []);

  /** keyboards — клавиатуры с авто-ID (их можно выбрать источником). */
  let keyboards = $derived(
    devices.filter((d) => d.kinds.includes("keyboard") && autoIds[d.info.path]),
  );

  /** setKey назначает строке id клавишу key ("" — убрать). */
  function setKey(id: string, key: string): void {
    rows = rows.map((r) => (r.id === id ? { ...r, key } : r));
  }

  /** label — понятное название строки: направление стика или кнопка с пояснением. */
  function label(r: Row): string {
    if (r.stick) return t(`gpw.stick.${r.id}`);
    const hint = t(`gpw.btn.${r.control}`);
    return hint.startsWith("gpw.") ? r.control : `${r.control} — ${hint}`;
  }

  /** padOK — имя геймпада по правилам: буквы, цифры и «_», начинается с буквы. */
  let padOK = $derived(/^\p{L}[\p{L}\p{N}_]*$/u.test(pad));

  /** create создаёт выключенный проект с геймпадом и привязками. */
  async function create(): Promise<void> {
    busy = true;
    try {
      const text = buildProject(
        { name, pad, template, device, rows, mouseRight, hide, rampMS: smooth ? 120 : 0 },
        { header: t("gpw.file_header"), mouse: t("gpw.file_mouse") },
      );
      const r = await api.importProject("gamepad2", text);
      created = r.id;
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = false;
    }
  }

  /** enable включает созданный проект: появляется виртуальный геймпад. */
  async function enable(): Promise<void> {
    try {
      await api.setProjectEnabled(created, true);
      toast(t("gpw.enabled", { pad }));
      onclose();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }
</script>

<Modal title={t("gpw.title")} wide {onclose}>
  {#if created}
    <!-- Готово: что дальше -->
    <div class="note ok">{t("gpw.done", { name, pad })}</div>
    <p>{t("gpw.done_hint")}</p>
    {#if hide}<p class="muted">{t("gpw.done_hide")}</p>{/if}
  {:else}
    <p>{t("gpw.intro")}</p>

    <!-- Что будет геймпадом и каким -->
    <div class="grid">
      <label for="gpw-device">{t("gpw.device")}</label>
      <select id="gpw-device" bind:value={device}>
        <option value="">{t("gpw.device_any")}</option>
        {#each keyboards as d (d.info.path)}
          <option value={autoIds[d.info.path]}>{autoIds[d.info.path]} — {d.info.name}</option>
        {/each}
      </select>
      <label for="gpw-template">{t("gpw.template")}</label>
      <select id="gpw-template" bind:value={template}>
        {#each GAMEPADS.filter((g) => templates.some((x) => x.id === g)) as g (g)}
          <option value={g}>{t("vdev.template." + g)}</option>
        {/each}
      </select>
      <label for="gpw-name">{t("gpw.name")}</label>
      <input id="gpw-name" bind:value={name} />
      <label for="gpw-pad">{t("gpw.pad")}</label>
      <span>
        <input id="gpw-pad" bind:value={pad} class:bad={!padOK} />
        <span class="muted">{t("gpw.pad_hint", { pad })}</span>
      </span>
    </div>

    <!-- Раскладка: кнопка геймпада ← клавиша -->
    <h3>{t("gpw.layout")}</h3>
    <p class="muted">{t("gpw.layout_hint")}</p>
    <div class="rows">
      {#each rows as r (r.id)}
        {#if !(mouseRight && isRightStick(r))}
          <span class="what">{label(r)}</span>
          <span class="row">
            <KeyCapture
              value={r.key}
              placeholder={t("gpw.none")}
              onchange={(v) => setKey(r.id, v)}
            />
            {#if r.key}
              <button class="small ghost" onclick={() => setKey(r.id, "")}>{t("gpw.unset")}</button>
            {/if}
          </span>
        {/if}
      {/each}
    </div>

    <!-- Настройки -->
    <label class="check"
      ><input type="checkbox" bind:checked={mouseRight} />
      <span><b>{t("gpw.mouse")}</b><br /><span class="muted">{t("gpw.mouse_hint")}</span></span
      ></label
    >
    <label class="check"
      ><input type="checkbox" bind:checked={smooth} />
      <span><b>{t("gpw.smooth")}</b><br /><span class="muted">{t("gpw.smooth_hint")}</span></span
      ></label
    >
    <label class="check"
      ><input type="checkbox" bind:checked={hide} />
      <span><b>{t("gpw.hide")}</b><br /><span class="muted">{t("gpw.hide_hint")}</span></span
      ></label
    >
  {/if}

  {#snippet footer()}
    {#if created}
      <button onclick={() => navigate("editor", created)}>{t("gpw.open")}</button>
      <button onclick={onclose}>{t("common.close")}</button>
      <button class="primary" onclick={() => void enable()}>{t("gpw.enable")}</button>
    {:else}
      <button onclick={onclose}>{t("common.cancel")}</button>
      <button
        class="primary"
        disabled={busy || !padOK || !name.trim()}
        onclick={() => void create()}>{t("gpw.create")}</button
      >
    {/if}
  {/snippet}
</Modal>

<style>
  .grid {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 10px 14px;
    align-items: center;
    margin: 12px 0;
  }
  .rows {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 4px 14px;
    align-items: center;
    margin-bottom: 12px;
  }
  .row {
    display: flex;
    gap: 6px;
    align-items: center;
  }
  .check {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    margin: 8px 0;
  }
  .check input {
    margin-top: 4px;
  }
  .bad {
    border-color: var(--danger);
  }
  h3 {
    margin: 14px 0 4px;
  }
</style>
