<!--
  Toasts — всплывающие сообщения внизу окна (lib/toast.svelte.ts). Props: нет.
-->
<script lang="ts">
  import { toasts } from "../toast.svelte";
</script>

<div class="toasts" aria-live="polite">
  {#each toasts as item (item.id)}
    <div class="toast {item.kind}">{item.text}</div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    bottom: 16px;
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 100;
    max-width: calc(100vw - 32px);
  }
  .toast {
    padding: 10px 16px;
    border-radius: var(--radius-s);
    background: var(--text);
    color: var(--bg);
    box-shadow: var(--shadow);
  }
  .toast.error {
    background: var(--danger);
    color: #fff;
  }
</style>
