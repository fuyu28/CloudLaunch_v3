<script lang="ts">
  import { HomeService } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import type { HomeView } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";

  let view = $state<HomeView | null>(null);
  let loadError = $state("");

  HomeService.Load()
    .then((v) => (view = v))
    .catch((e) => (loadError = String(e)));
</script>

<main>
  {#if view?.banner}
    <div class="banner" role="alert">{view.banner}</div>
  {/if}

  <h1>ゲーム</h1>

  {#if loadError}
    <p class="error">一覧を読み込めませんでした: {loadError}</p>
  {:else if view === null}
    <p>読み込み中…</p>
  {:else if view.games.length === 0}
    <p>登録したゲームはありません</p>
  {:else}
    <ul class="games">
      {#each view.games as game (game.id)}
        <li class="card">
          <span class="title">{game.title}</span>
          {#if game.brand}
            <span class="brand">{game.brand}</span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</main>

<style>
  :global(body) {
    margin: 0;
    font-family: system-ui, "Yu Gothic UI", "Noto Sans CJK JP", sans-serif;
    background: #f6f6f7;
    color: #1d1d1f;
  }
  main {
    padding: 16px 24px;
  }
  .banner {
    margin: -16px -24px 16px;
    padding: 8px 24px;
    background: #fff4d6;
    border-bottom: 1px solid #e8c766;
  }
  .error {
    color: #b3261e;
  }
  .games {
    list-style: none;
    padding: 0;
    display: grid;
    gap: 8px;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 12px 16px;
    background: #fff;
    border: 1px solid #e0e0e3;
    border-radius: 8px;
  }
  .title {
    font-weight: 600;
  }
  .brand {
    font-size: 0.875em;
    color: #6e6e73;
  }
</style>
