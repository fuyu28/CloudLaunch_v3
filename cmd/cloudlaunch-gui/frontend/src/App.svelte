<script lang="ts">
  import { Events } from "@wailsio/runtime";
  import {
    BackupService,
    HomeService,
    SyncService,
  } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import type {
    BackupView,
    DialogView,
    HomeView,
    ProgressEvent,
    TransferDirection,
  } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import { SyncProgress } from "../bindings/events";
  import Settings from "./Settings.svelte";

  type CardState = { status: string; error: string };
  type Transfer = { title: string; progress: ProgressEvent | null; cancelError: string };

  let view = $state<HomeView | null>(null);
  let loadError = $state("");
  let cards = $state<Record<string, CardState>>({});
  let checking = $state<Record<string, boolean>>({});
  let transfers = $state<Record<string, Transfer>>({});
  // dialogs は、最後の確認で受け取ったダイアログ。変更なしのときは開かずに持ち、カードの「上書き…」から開く
  let dialogs = $state<Record<string, DialogView>>({});
  let dialog = $state<{ gameId: string; title: string; view: DialogView } | null>(null);
  // backupNotes は、バックアップのフォルダを開いたときの案内。確認の結果の行とは別に出し、次の確認か転送で消す
  let backupNotes = $state<Record<string, BackupView>>({});
  let openingBackups = $state<Record<string, boolean>>({});
  // backupSeqs は、案内を消した後に届いた古い応答で、案内を出し直さないための番号。表示に使わないので $state にしない
  const backupSeqs: Record<string, number> = {};

  function clearBackupNote(gameId: string) {
    backupSeqs[gameId] = (backupSeqs[gameId] ?? 0) + 1;
    delete backupNotes[gameId];
  }

  // page は、転送の帯と進捗を設定画面でも出し続けるよう、App の中で画面だけを切り替える
  let page = $state<"home" | "settings">("home");

  function loadHome() {
    HomeService.Load()
      .then((v) => {
        view = v;
        loadError = "";
      })
      .catch((e) => (loadError = String(e)));
  }
  loadHome();

  function backToHome() {
    page = "home";
    // 設定を保存すると、Go は確認済みの plan を捨てる。古い「上書き…」から開いても、承認は通らないので出さない
    dialogs = {};
    loadHome();
  }

  Events.On(SyncProgress, (ev) => {
    const t = transfers[ev.data.gameId];
    if (t) t.progress = ev.data;
  });

  async function check(gameId: string, title: string) {
    checking[gameId] = true;
    clearBackupNote(gameId);
    // 新しい確認を始めると、Go は前の確認の plan を捨てる
    delete dialogs[gameId];
    try {
      const v = await SyncService.Check(gameId);
      // 置き換えた新しい確認が、まだ走っている
      if (v.superseded) return;
      checking[gameId] = false;
      cards[gameId] = { status: v.status, error: v.error };
      if (v.dialog) {
        dialogs[gameId] = v.dialog;
        if (v.dialog.open) dialog = { gameId, title, view: v.dialog };
      }
    } catch (e) {
      checking[gameId] = false;
      cards[gameId] = { status: "", error: String(e) };
    }
  }

  async function openBackups(gameId: string) {
    openingBackups[gameId] = true;
    const seq = (backupSeqs[gameId] = (backupSeqs[gameId] ?? 0) + 1);
    let note: BackupView;
    try {
      note = await BackupService.OpenBackups(gameId);
    } catch (e) {
      note = { status: "", error: String(e) };
    } finally {
      openingBackups[gameId] = false;
    }
    if (backupSeqs[gameId] === seq) backupNotes[gameId] = note;
  }

  function cancel(gameId: string) {
    SyncService.Cancel(gameId).catch((e) => {
      const t = transfers[gameId];
      if (t) t.cancelError = "キャンセルできませんでした: " + String(e);
    });
  }

  async function approve(direction: TransferDirection) {
    if (!dialog) return;
    const { gameId, title, view } = dialog;
    dialog = null;
    delete dialogs[gameId];
    clearBackupNote(gameId);
    transfers[gameId] = { title, progress: null, cancelError: "" };
    try {
      const v = await SyncService.Transfer(gameId, view.token, direction);
      cards[gameId] = { status: v.status, error: v.error };
    } catch (e) {
      cards[gameId] = { status: "", error: String(e) };
    } finally {
      delete transfers[gameId];
    }
  }

</script>

{#if page === "settings"}
  <Settings onBack={backToHome} />
{:else}
<main>
  {#if view?.banner}
    <div class="banner" role="alert">{view.banner}</div>
  {/if}
  {#if view?.settingsNotice}
    <div class="banner settings-notice" role="alert">
      <span>{view.settingsNotice}</span>
      <button onclick={() => (page = "settings")}>設定を開く</button>
    </div>
  {/if}

  <div class="top">
    <h1>ゲーム</h1>
    <button onclick={() => (page = "settings")}>設定</button>
  </div>

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
            <div class="buttons">
              <button disabled={openingBackups[game.id]} onclick={() => openBackups(game.id)}>バックアップ</button>
              <button disabled={checking[game.id] || !!transfer} onclick={() => check(game.id, game.title)}>
                {checking[game.id] ? "確認中…" : "確認"}
              </button>
            </div>
          </div>
          {#if transfer}
            <progress max="100" value={transfer.progress?.percent ?? 0}></progress>
            <div class="status">{transfer.progress?.text ?? "準備中…"}</div>
          {:else if card}
            {#if card.status}<div class="status">{card.status}</div>{/if}
            {#if card.error}<div class="error">{card.error}</div>{/if}
            {#if dialogs[game.id] && !checking[game.id]}
              <div>
                <button onclick={() => (dialog = { gameId: game.id, title: game.title, view: dialogs[game.id] })}>
                  上書き…
                </button>
              </div>
            {/if}
          {/if}
          {#if backupNotes[game.id]?.status}<div class="status">{backupNotes[game.id].status}</div>{/if}
          {#if backupNotes[game.id]?.error}<div class="error">{backupNotes[game.id].error}</div>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</main>
{/if}

{#if Object.keys(transfers).length > 0}
  <footer class="transfers">
    {#each Object.entries(transfers) as [gameId, t] (gameId)}
      <div class="transfer">
        <span class="title">{t.title}</span>
        <span>{t.progress?.text ?? "準備中…"}</span>
        <button onclick={() => cancel(gameId)}>キャンセル</button>
      </div>
      {#if t.cancelError}<div class="error">{t.cancelError}</div>{/if}
    {/each}
  </footer>
{/if}

{#if dialog}
  <div class="backdrop">
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title">
      <h2 id="dialog-title">{dialog.title}: {dialog.view.title}</h2>
      {#if dialog.view.warning}<p class="error">{dialog.view.warning}</p>{/if}
      <table>
        <thead>
          <tr><th></th><th>この PC</th><th>リモート</th></tr>
        </thead>
        <tbody>
          {#each dialog.view.compare as row (row.label)}
            <tr><th>{row.label}</th><td>{row.local}</td><td>{row.remote}</td></tr>
          {/each}
        </tbody>
      </table>
      <ul class="choices">
        {#each dialog.view.actions as action (action.direction)}
          <li>
            <button class:primary={action.primary} disabled={action.disabled} onclick={() => approve(action.direction)}>
              {action.label}
            </button>
            <span class="note">{action.note}</span>
          </li>
        {/each}
      </ul>
      <div class="actions">
        <button onclick={() => (dialog = null)}>やめる</button>
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
  .settings-notice {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
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
  .buttons {
    display: flex;
    gap: 8px;
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
  .dialog td {
    padding-right: 16px;
  }
  .choices {
    list-style: none;
    padding: 0;
    margin: 16px 0 0;
    display: grid;
    gap: 8px;
  }
  .choices li {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .note {
    font-size: 0.875em;
    color: #6e6e73;
  }
</style>
