<!--
  MacroInput — поле текста макроса DSL с проверкой: ошибка показывается сразу под полем
  с местом ошибки (FR-UI-4). Проверка — разбором на сервере (единый разборщик DSL).
  Props: value — текст макроса; onchange(value) — новый текст.
-->
<script lang="ts">
  import { api, ApiError } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";

  let { value, onchange }: { value: string; onchange: (v: string) => void } = $props();

  /** error — текст ошибки разбора; pos — её место. */
  let error = $state("");
  let pos = $state<{ line: number; col: number } | null>(null);

  /** check проверяет макрос разбором на сервере. */
  async function check(text: string): Promise<void> {
    try {
      await api.dslToActions(text);
      error = "";
      pos = null;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      pos = e instanceof ApiError ? (e.details?.pos ?? null) : null;
    }
  }

  // Проверяем текст при показе и после каждого изменения.
  $effect(() => {
    void check(value);
  });
</script>

<span class="macro">
  <input
    class:bad={error !== ""}
    {value}
    spellcheck="false"
    placeholder={"^{Ctrl}{C}~{Ctrl}"}
    onchange={(e) => onchange(e.currentTarget.value)}
  />
  {#if error}
    <span class="err">
      {#if pos}{t("editor.macro_error_at", { col: pos.col })}{/if}
      {error}
    </span>
  {/if}
</span>

<style>
  .macro {
    display: inline-flex;
    flex-direction: column;
    gap: 2px;
    min-width: 260px;
    flex: 1;
  }
  input {
    font-family: var(--mono);
  }
  .bad {
    border-color: var(--danger);
  }
  .err {
    color: var(--danger);
    font-size: 0.85rem;
  }
</style>
