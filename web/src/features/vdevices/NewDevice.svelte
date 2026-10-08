<!--
  NewDevice — мастер «Новое виртуальное устройство» (FR-VD-4, FR-VD-8): 1) какое устройство
  (геймпад, руль, лётный джойстик…), 2) чем управлять (клавиатурой, клавиатурой и мышью,
  настоящим геймпадом) и готовая раскладка, которую можно поменять, 3) готово — что дальше и
  «Включить и проверить». mKey создаёт обычный выключенный проект с устройством и раскладкой.
  Props: cards — уже существующие устройства (их имена заняты); templates — состав шаблонов;
  devices — устройства ввода; autoIds — авто-ID устройств по пути; onclose() — закрыть;
  ontest(name, project) — включить созданное устройство и открыть проверку.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { navigate } from "../../lib/router.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { InputDevice, VirtualCard, VirtualTemplateInfo } from "../../lib/types";
  import {
    buildProject,
    freeName,
    hidesByDefault,
    KINDS,
    NAME_RE,
    presetRows,
    schemesFor,
    type Row,
    type Scheme,
  } from "./presets";
  import RowsEditor from "./RowsEditor.svelte";

  let {
    cards,
    templates,
    devices,
    autoIds,
    onclose,
    ontest,
  }: {
    cards: VirtualCard[];
    templates: VirtualTemplateInfo[];
    devices: InputDevice[];
    autoIds: Record<string, string>;
    onclose: () => void;
    ontest: (name: string, project: string) => void;
  } = $props();

  /** step — шаг мастера; kind — вид устройства. */
  let step = $state<"kind" | "setup" | "done">("kind");
  let kind = $state("xbox360");

  /** Выбор на втором шаге: способ управления, источник, имена, настройки. */
  let scheme = $state<Scheme>("keyboard");
  let device = $state("");
  let project = $state("");
  let name = $state("");
  let smooth = $state(false);
  let hide = $state(true);
  /** busy — идёт создание; created — ID созданного проекта. */
  let busy = $state(false);
  let created = $state("");

  /** kinds — виды, которые знает этот mKey (по шаблонам из API). */
  let kinds = $derived(KINDS.filter((k) => templates.some((x) => x.id === k.id)));
  /** info — состав выбранного вида. */
  let info = $derived(templates.find((x) => x.id === kind));

  // Раскладка по умолчанию — при смене вида, способа или плавности (правки человека — поверх неё).
  let rows = $derived<Row[]>(presetRows(kind, scheme, info, smooth ? 120 : 0));

  /** sources — устройства-источники для выбранного способа: геймпады или клавиатуры с авто-ID. */
  let sources = $derived(
    devices.filter(
      (d) =>
        autoIds[d.info.path] &&
        (scheme === "gamepad" ? d.kinds.includes("gamepad") : d.kinds.includes("keyboard")),
    ),
  );

  /** taken — имя уже занято другим устройством; nameOK — имя по правилам и свободно. */
  let taken = $derived(cards.some((c) => c.name.toLowerCase() === name.toLowerCase()));
  let nameOK = $derived(NAME_RE.test(name) && !taken);
  /** ready — можно создавать: имена в порядке, для геймпада выбран геймпад. */
  let ready = $derived(nameOK && project.trim() !== "" && (scheme !== "gamepad" || device !== ""));

  /** example — пример записи в макросах для подсказки: первая кнопка или ось устройства. */
  let example = $derived(`{${name || "dev"}.${info?.buttons[0] ?? info?.axes[0] ?? "…"}}`);

  /** choose выбирает вид и переходит к настройке с подходящими значениями по умолчанию. */
  function choose(id: string): void {
    kind = id;
    scheme = schemesFor(id)[0] ?? "none";
    device = "";
    project = t(`vd.default_project.${id}`);
    name = freeName(
      id,
      cards.map((c) => c.name),
    );
    smooth = false;
    hide = hidesByDefault(id, scheme);
    step = "setup";
  }

  /** setScheme меняет способ управления: источник сбрасывается, «прятать» — по умолчанию для него. */
  function setScheme(s: Scheme): void {
    scheme = s;
    device = "";
    hide = hidesByDefault(kind, s);
  }

  /** create создаёт выключенный проект с устройством и раскладкой. */
  async function create(): Promise<void> {
    busy = true;
    try {
      const text = buildProject(
        { project, name, kind, device, hide, rows: scheme === "none" ? [] : rows },
        t("vd.file_header"),
      );
      created = (await api.importProject(project, text)).id;
      step = "done";
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = false;
    }
  }
</script>

<Modal title={t("vd.new_title")} wide {onclose}>
  {#if step === "kind"}
    <!-- Шаг 1: какое устройство -->
    <p>{t("vd.step_kind")}</p>
    <div class="kinds">
      {#each kinds as k (k.id)}
        <button class="kind" onclick={() => choose(k.id)}>
          <span class="icon" aria-hidden="true">{k.icon}</span>
          <span class="text">
            <b>{t(`vd.kind.${k.id}`)}</b>
            <span class="muted">{t(`vd.kind.${k.id}.desc`)}</span>
          </span>
        </button>
      {/each}
    </div>
  {:else if step === "setup"}
    <!-- Шаг 2: чем управлять, имена, раскладка -->
    <h3>{t(`vd.kind.${kind}`)}</h3>
    {#if schemesFor(kind)[0] !== "none"}
      <fieldset class="schemes">
        <legend>{t("vd.scheme")}</legend>
        {#each schemesFor(kind) as s (s)}
          <label class="row"
            ><input
              type="radio"
              name="vd-scheme"
              checked={scheme === s}
              onchange={() => setScheme(s)}
            />
            {t(`vd.scheme.${s}`)}</label
          >
        {/each}
      </fieldset>
    {/if}

    <div class="grid">
      {#if scheme !== "none"}
        <label for="vd-source"
          >{t(scheme === "gamepad" ? "vd.source_gamepad" : "vd.source_keyboard")}</label
        >
        <span>
          <select id="vd-source" bind:value={device}>
            <option value=""
              >{t(scheme === "gamepad" ? "vd.pick_gamepad" : "vd.source_any_keyboard")}</option
            >
            {#each sources as d (d.info.path)}
              <option value={autoIds[d.info.path]}>{autoIds[d.info.path]} — {d.info.name}</option>
            {/each}
          </select>
          {#if scheme === "gamepad" && sources.length === 0}
            <span class="muted">{t("vd.no_gamepad")}</span>
          {/if}
        </span>
      {/if}
      <label for="vd-project">{t("vd.project_name")}</label>
      <input id="vd-project" bind:value={project} />
      <label for="vd-name">{t("vd.dev_name")}</label>
      <span>
        <input id="vd-name" bind:value={name} class:bad={!nameOK} />
        <span class="muted"
          >{taken ? t("vd.dev_name_taken") : t("vd.dev_name_hint", { example, name })}</span
        >
      </span>
    </div>

    <!-- Пояснения к управлению мышью; у видов без раскладки — как ими управлять -->
    {#if kind === "wheel" && scheme === "keyboard_mouse"}
      <p class="note">{t("vd.mouse_steer")}</p>
    {:else if kind === "flightstick" && scheme === "keyboard_mouse"}
      <p class="note">{t("vd.mouse_stick")}</p>
    {:else if scheme === "none"}
      <p class="note">{t("vd.no_layout_kind", { example })}</p>
    {/if}

    {#if scheme !== "none"}
      <h3>{t("vd.layout")}</h3>
      <p class="muted">{t("vd.layout_hint")}</p>
      <RowsEditor {rows} {device} onchange={(r) => (rows = r)} />

      {#if kind === "xbox360" || kind === "ds4"}
        <label class="check"><input type="checkbox" bind:checked={smooth} /> {t("vd.smooth")}</label
        >
      {/if}
      <label class="check"
        ><input type="checkbox" bind:checked={hide} />
        <span><b>{t("vd.hide")}</b><br /><span class="muted">{t("vd.hide_hint")}</span></span
        ></label
      >
    {/if}
  {:else}
    <!-- Шаг 3: готово -->
    <div class="note ok">{t("vd.done", { name, project })}</div>
    <p>{t("vd.done_steps")}</p>
    <p class="muted">{t("vd.emergency")}</p>
  {/if}

  {#snippet footer()}
    {#if step === "setup"}
      <button onclick={() => (step = "kind")}>{t("vd.back")}</button>
      <button class="primary" disabled={busy || !ready} onclick={() => void create()}
        >{t("vd.create")}</button
      >
    {:else if step === "done"}
      <button onclick={() => navigate("editor", created)}>{t("gpw.open")}</button>
      <button onclick={onclose}>{t("common.close")}</button>
      <button class="primary" onclick={() => ontest(name, created)}>{t("vd.enable_test")}</button>
    {:else}
      <button onclick={onclose}>{t("common.cancel")}</button>
    {/if}
  {/snippet}
</Modal>

<style>
  .kinds {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr));
    gap: 10px;
  }
  /* Описание переносится по словам (у кнопок по умолчанию текст в одну строку). */
  .kind {
    white-space: normal;
    display: flex;
    gap: 12px;
    align-items: flex-start;
    text-align: left;
    padding: 12px;
    height: 100%;
  }
  .icon {
    font-size: 1.8rem;
    line-height: 1;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }
  .schemes {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 18px;
    border: none;
    padding: 0;
    margin: 8px 0;
  }
  .schemes legend {
    padding: 0;
    margin-bottom: 4px;
    font-weight: 600;
  }
  .grid {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 10px 14px;
    align-items: center;
    margin: 12px 0;
  }
  .grid span {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 10px;
    align-items: center;
  }
  .check {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    margin: 10px 0;
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
  @media (max-width: 560px) {
    .grid {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
