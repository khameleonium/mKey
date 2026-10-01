<!--
  PlacesList — все места, где mKey хранит файлы пользователя («Где что лежит», раздел «Настройки»):
  настройки, проекты, записи, скрипты, журнал — с описанием и кнопкой «Скопировать путь».
  Props нет: список приходит от демона (GET /places).
-->
<script lang="ts">
  import { loadPlaces, places } from "../places.svelte";
  import PlaceHint from "./PlaceHint.svelte";

  // Список загружается при первом показе (и после смены языка).
  $effect(() => {
    void loadPlaces();
  });
</script>

{#each places() as p (p.id)}
  <div class="item">
    <b>{p.name}</b>
    <PlaceHint id={p.id} />
  </div>
{/each}

<style>
  .item + .item {
    margin-top: 0.8rem;
  }
</style>
