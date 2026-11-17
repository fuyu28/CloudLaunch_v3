<script lang="ts">
  import { Events } from "@wailsio/runtime";
  import { HomeService, SyncService } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import type { ConfirmView, HomeView } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import type { Progress } from "../bindings/github.com/fuyu28/CloudLaunch_v3/internal/syncore";
  import { SyncProgress } from "../bindings/events";

  type CardState = { status: string; error: string };
  type Transfer = { title: string; progress: Progress | null };

  let view = $state<HomeView | null>(null);
  let loadError = $state("");
  let cards = $state<Record<string, CardState>>({});
  let checking = $state<Record<string, boolean>>({});
  let transfers = $state<Record<string, Transfer>>({});
  let dialog = $state<{ gameId: string; title: string; confirm: ConfirmView } | null>(null);

  HomeService.Load()
    .then((v) => (view = v))
    .catch((e) => (loadError = String(e)));

  Events.On(SyncProgress, (ev) => {
    const t = transfers[ev.data.gameId];
    if (t) t.progress = ev.data.progress;
  });

  async function check(gameId: string, title: string) {
    checking[gameId] = true;
    try {
      const v = await SyncService.Check(gameId);
      if (v.superseded) return;
      cards[gameId] = { status: v.status, error: v.error };
      if (v.confirm) dialog = { gameId, title, confirm: v.confirm };
    } catch (e) {
      cards[gameId] = { status: "", error: String(e) };
    } finally {
      checking[gameId] = false;
    }
  }

  // 確認ダイアログは承認した時点で閉じ、進捗はカードと画面下の帯に出す
  async function approve() {
    if (!dialog) return;
    const { gameId, title, confirm } = dialog;
    dialog = null;
    transfers[gameId] = { title, progress: null };
    try {
      const v = await SyncService.Transfer(gameId, confirm.token);
      cards[gameId] = { status: v.status, error: v.error };
    } catch (e) {
      cards[gameId] = { status: "", error: String(e) };
    } finally {
      delete transfers[gameId];
    }
  }

  function percent(p: Progress | null): number {
    if (!p || p.BytesTotal === 0) return p && p.FilesDone === p.FilesTotal ? 100 : 0;
    return Math.floor((p.BytesDone / p.BytesTotal) * 100);
  }

  function progressText(p: Progress | null): string {
    if (!p) return "準備中…";
    return `${p.FilesDone} / ${p.FilesTotal} ファイル（${percent(p)}%）`;
  }
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
        {@const card = cards[game.id]}
        {@const transfer = transfers[game.id]}
        <li class="card">
          <div class="head">
            <div>
              <div class="title">{game.title}</div>
              {#if game.brand}
                <div class="brand">{game.brand}</div>
              {/if}
            </div>
            <button disabled={checking[game.id] || !!transfer} onclick={() => check(game.id, game.title)}>
              {checking[game.id] ? "確認中…" : "確認"}
            </button>
          </div>
          {#if transfer}
            <progress max="100" value={percent(transfer.progress)}></progress>
            <div class="status">{progressText(transfer.progress)}</div>
          {:else if card}
            {#if card.status}<div class="status">{card.status}</div>{/if}
            {#if card.error}<div class="error">{card.error}</div>{/if}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</main>

{#if Object.keys(transfers).length > 0}
  <footer class="transfers">
    {#each Object.entries(transfers) as [gameId, t] (gameId)}
      <div class="transfer">
        <span class="title">{t.title}</span>
        <span>{progressText(t.progress)}</span>
        <button onclick={() => SyncService.Cancel(gameId)}>キャンセル</button>
      </div>
    {/each}
  </footer>
{/if}

{#if dialog}
  <div class="backdrop">
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title">
      <h2 id="dialog-title">{dialog.title}: {dialog.confirm.title}</h2>
      <table>
        <tbody>
          {#each dialog.confirm.rows as row (row.label)}
            <tr><th>{row.label}</th><td>{row.value}</td></tr>
          {/each}
        </tbody>
      </table>
      <div class="actions">
        <button onclick={() => (dialog = null)}>やめる</button>
        <button class="primary" onclick={approve}>{dialog.confirm.approveLabel}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  :global(body) {
    margin: 0;
    font-family: system-ui, "Yu Gothic UI", "Noto Sans CJK JP", sans-serif;
    background: #f6f6f7;
    color: #1d1d1f;
  }
  main {
    padding: 16px 24px 80px;
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
    gap: 6px;
    padding: 12px 16px;
    background: #fff;
    border: 1px solid #e0e0e3;
    border-radius: 8px;
  }
  .head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
  }
  .title {
    font-weight: 600;
  }
  .brand,
  .status {
    font-size: 0.875em;
    color: #6e6e73;
  }
  progress {
    width: 100%;
  }
  .transfers {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    padding: 8px 24px;
    background: #fff;
    border-top: 1px solid #e0e0e3;
  }
  .transfer {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .transfer > span:nth-child(2) {
    flex: 1;
  }
  .backdrop {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgb(0 0 0 / 0.3);
  }
  .dialog {
    min-width: 360px;
    padding: 16px 20px;
    background: #fff;
    border-radius: 8px;
  }
  .dialog h2 {
    margin-top: 0;
    font-size: 1.1em;
  }
  .dialog th {
    padding-right: 16px;
    text-align: left;
    font-weight: normal;
    color: #6e6e73;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
  }
  .primary {
    font-weight: 600;
  }
</style>
