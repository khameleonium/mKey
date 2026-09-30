<!--
  KeyCapture — поле клавиши с кнопкой «Нажмите клавишу…»: ловит настоящее нажатие на любом
  устройстве через API (FR-UI-3). Имя можно и вписать вручную.
  Props: value — имя одной клавиши ("F8") или, при combo, сочетание записью зажатием
  ("^{Ctrl}^{Alt}{H}", одна клавиша — "{F8}"); combo — ловить сочетание (все клавиши до первого
  отпускания; для горячих клавиш); onchange(value) — новое значение.
-->
<script lang="ts">
  import { api, ApiError } from "../api";
  import { t } from "../i18n/index.svelte";

  let {
    value,
    combo = false,
    onchange,
  }: {
    value: string;
    combo?: boolean;
    onchange: (value: string) => void;
  } = $props();

  /** waiting — ждём нажатия; error — текст ошибки последней попытки. */
  let waiting = $state(false);
  let error = $state("");

  /** capture ждёт нажатия и подставляет его имя. */
  async function capture(): Promise<void> {
    waiting = true;
    error = "";
    try {
      const k = await api.captureKey(combo);
      onchange(k.name);
    } catch (e) {
      // Время вышло или ввод недоступен — понятное объяснение.
      if (e instanceof ApiError && e.status === 408) error = t("key.timeout");
      else if (e instanceof ApiError && e.status === 503) error = t("key.no_input");
      else error = e instanceof Error ? e.message : String(e);
    } finally {
      waiting = false;
    }
  }
</script>

<span class="key">
  <!-- Ручной ввод имени -->
  <input
    class="name"
    {value}
    placeholder={combo ? "^{Ctrl}{H}" : "Enter"}
    spellcheck="false"
    onchange={(e) => onchange(e.currentTarget.value)}
  />
  <!-- Захват настоящего нажатия -->
  <button class="small" class:waiting disabled={waiting} onclick={capture}>
    ⌨ {waiting ? t(combo ? "key.waiting_combo" : "key.waiting") : t("key.capture")}
  </button>
  {#if error}<span class="err">{error}</span>{/if}
</span>

<style>
  .key {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
  }
  .name {
    width: 11em;
    font-family: var(--mono);
  }
  .waiting {
    border-color: var(--accent);
    background: var(--accent-soft);
    animation: pulse 1s infinite alternate;
  }
  .err {
    color: var(--danger);
    font-size: 0.88rem;
  }
  @keyframes pulse {
    to {
      opacity: 0.6;
    }
  }
</style>
