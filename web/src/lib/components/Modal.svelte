<!--
  Modal — диалоговое окно поверх страницы. Закрывается кнопкой «×», клавишей Esc
  и щелчком по затемнённому фону.
  Props: title — заголовок; wide — широкое окно; onclose() — закрыть; children — содержимое;
  footer — кнопки внизу (необязательно).
-->
<script lang="ts">
  import type { Snippet } from "svelte";
  import { t } from "../i18n/index.svelte";

  let {
    title,
    wide = false,
    onclose,
    children,
    footer,
  }: {
    title: string;
    wide?: boolean;
    onclose: () => void;
    children: Snippet;
    footer?: Snippet;
  } = $props();

  /** dialog — элемент окна; открывается модально сразу после появления. */
  let dialog: HTMLDialogElement;
  $effect(() => {
    dialog.showModal();
  });
</script>

<!-- Нативный <dialog>: фокус внутри окна, Esc закрывает (событие cancel) -->
<dialog
  bind:this={dialog}
  class:wide
  oncancel={(e) => {
    e.preventDefault();
    onclose();
  }}
  onclick={(e) => {
    if (e.target === dialog) onclose();
  }}
>
  <div class="box">
    <header class="row">
      <h2>{title}</h2>
      <span class="spacer"></span>
      <button class="ghost small" aria-label={t("common.close")} onclick={onclose}>✕</button>
    </header>
    <div class="body">{@render children()}</div>
    {#if footer}
      <footer class="row">{@render footer()}</footer>
    {/if}
  </div>
</dialog>

<style>
  dialog {
    border: none;
    padding: 0;
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    box-shadow: 0 10px 40px rgb(0 0 0 / 30%);
    width: min(560px, calc(100vw - 32px));
    max-height: calc(100vh - 48px);
  }
  dialog.wide {
    width: min(900px, calc(100vw - 32px));
  }
  dialog::backdrop {
    background: rgb(10 15 25 / 45%);
  }
  .box {
    padding: 16px 20px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  h2 {
    margin: 0;
  }
  footer {
    justify-content: flex-end;
  }
</style>
