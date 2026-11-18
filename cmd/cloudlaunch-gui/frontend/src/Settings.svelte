<script lang="ts">
  import { SettingsService } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import type { SaveView, SettingsView } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";

  let { onBack }: { onBack: () => void } = $props();

  let view = $state<SettingsView | null>(null);
  let loadError = $state("");
  let remote = $state({ endpoint: "", region: "", bucket: "", accessKeyId: "", secretAccessKey: "" });
  let deviceName = $state("");
  let remoteResult = $state<SaveView | null>(null);
  let deviceResult = $state<SaveView | null>(null);
  let saving = $state(false);

  async function load() {
    try {
      const v = await SettingsService.Load();
      view = v;
      if (v.error) return;
      remote = { endpoint: v.endpoint, region: v.region, bucket: v.bucket, accessKeyId: v.accessKeyId, secretAccessKey: "" };
      deviceName = v.deviceName;
    } catch (e) {
      loadError = String(e);
    }
  }
  load();

  async function save(call: () => Promise<SaveView>, setResult: (r: SaveView) => void) {
    saving = true;
    let r: SaveView;
    try {
      r = await call();
    } catch (e) {
      r = { status: "", error: String(e) };
    } finally {
      saving = false;
    }
    setResult(r);
    // 保存できたら、「保存済み」の表示と入力欄を、保存した値に揃える
    if (!r.error) await load();
  }
</script>

<main>
  <div class="top">
    <h1>設定</h1>
    <button onclick={onBack}>一覧へ戻る</button>
  </div>

  {#if loadError}
    <p class="error">設定を読み込めませんでした: {loadError}</p>
  {:else if view === null}
    <p>読み込み中…</p>
  {:else if view.error}
    <p class="error">{view.error}</p>
  {:else}
    <section>
      <h2>R2 の接続情報</h2>
      {#if view.envNotice}<p class="notice">{view.envNotice}</p>{/if}
      <label>
        エンドポイント
        <input bind:value={remote.endpoint} placeholder="https://<アカウント ID>.r2.cloudflarestorage.com" />
      </label>
      <label>
        バケット
        <input bind:value={remote.bucket} />
      </label>
      <label>
        Region
        <input bind:value={remote.region} placeholder="auto" />
        <span class="hint">通常は空（auto）のままでかまいません</span>
      </label>
      <label>
        アクセスキー ID
        <input bind:value={remote.accessKeyId} autocomplete="off" />
      </label>
      <label>
        シークレットアクセスキー
        <input
          type="password"
          bind:value={remote.secretAccessKey}
          autocomplete="off"
          placeholder={view.secretSaved ? "保存済み（変えるときだけ入れる）" : ""}
        />
        {#if view.secretSaved}
          <span class="hint">アクセスキー ID を変えるときは、シークレットも入れてください</span>
        {/if}
      </label>
      <div class="actions">
        <button
          disabled={saving}
          onclick={() => save(() => SettingsService.SaveRemote($state.snapshot(remote)), (r) => (remoteResult = r))}
        >
          接続情報を保存
        </button>
      </div>
      {#if remoteResult?.status}<p class="status">{remoteResult.status}</p>{/if}
      {#if remoteResult?.error}<p class="error">{remoteResult.error}</p>{/if}
    </section>

    <section>
      <h2>この PC の名前（deviceName）</h2>
      <label>
        deviceName
        <input bind:value={deviceName} />
        <span class="hint">確認ダイアログで、どの PC が保存したセーブかを表すのに使います</span>
      </label>
      <div class="actions">
        <button
          disabled={saving}
          onclick={() => save(() => SettingsService.SaveDeviceName(deviceName), (r) => (deviceResult = r))}
        >
          deviceName を保存
        </button>
      </div>
      {#if deviceResult?.status}<p class="status">{deviceResult.status}</p>{/if}
      {#if deviceResult?.error}<p class="error">{deviceResult.error}</p>{/if}
    </section>
  {/if}
</main>

<style>
  main {
    padding: 16px 24px 80px;
    max-width: 640px;
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  section {
    display: grid;
    gap: 10px;
    margin-bottom: 24px;
    padding: 12px 16px;
    background: #fff;
    border: 1px solid #e0e0e3;
    border-radius: 8px;
  }
  h2 {
    margin: 0;
    font-size: 1.05em;
  }
  label {
    display: grid;
    gap: 4px;
  }
  input {
    padding: 4px 6px;
  }
  .hint,
  .status {
    font-size: 0.875em;
    color: #6e6e73;
  }
  .notice {
    margin: 0;
    padding: 8px 12px;
    background: #fff4d6;
    border: 1px solid #e8c766;
    border-radius: 4px;
  }
  .error {
    color: #b3261e;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
  }
</style>
