<script lang="ts">
  import { SettingsService } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import type { SaveView, SettingsView } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";

  // onBack の saved は、この画面で 1 回でも保存できたか。保存すると Go は確認済みの plan を捨てる
  let { onBack }: { onBack: (saved: boolean) => void } = $props();

  type Section = "remote" | "device";

  let view = $state<SettingsView | null>(null);
  let loadError = $state("");
  let remote = $state({ endpoint: "", region: "", bucket: "", accessKeyId: "", secretAccessKey: "" });
  let deviceName = $state("");
  let remoteResult = $state<SaveView | null>(null);
  let deviceResult = $state<SaveView | null>(null);
  let saving = $state(false);
  let saved = false;

  // load は、入力欄のうち sections の欄だけを読んだ値に揃える。片方を保存した後に、もう片方の入力中の値を消さないため。
  // 読めなければ、そのエラーを投げる
  async function load(sections: Section[]) {
    const v = await SettingsService.Load();
    view = v;
    if (sections.includes("remote")) {
      remote = { endpoint: v.endpoint, region: v.region, bucket: v.bucket, accessKeyId: v.accessKeyId, secretAccessKey: "" };
    }
    if (sections.includes("device")) deviceName = v.deviceName;
  }
  load(["remote", "device"]).catch((e) => (loadError = String(e)));

  async function save(section: Section, call: () => Promise<SaveView>, setResult: (r: SaveView) => void) {
    saving = true;
    let r: SaveView;
    try {
      r = await call();
      if (!r.error) {
        saved = true;
        // 読み直せなくても保存はできているので、フォームを残して、結果の下に出す
        await load([section]).catch((e) => (r = { ...r, error: "保存した値を読み直せませんでした: " + String(e) }));
      }
    } catch (e) {
      r = { status: "", error: String(e) };
    } finally {
      saving = false;
    }
    setResult(r);
  }
</script>

<main>
  <div class="top">
    <h1>設定</h1>
    <!-- 保存中に戻ると、保存の結果を知らないまま一覧に戻り、保存で捨てられた plan のダイアログを残すので、終わるまで待たせる -->
    <button disabled={saving} onclick={() => onBack(saved)}>一覧へ戻る</button>
  </div>

  {#if loadError}
    <p class="error">設定を読み込めませんでした: {loadError}</p>
  {:else if view === null}
    <p>読み込み中…</p>
  {:else}
    <section>
      <h2>R2 の接続情報</h2>
      {#if view.remoteError}<p class="error">{view.remoteError}</p>{/if}
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
          onclick={() => save("remote", () => SettingsService.SaveRemote($state.snapshot(remote)), (r) => (remoteResult = r))}
        >
          接続情報を保存
        </button>
      </div>
      {#if remoteResult?.status}<p class="status">{remoteResult.status}</p>{/if}
      {#if remoteResult?.error}<p class="error">{remoteResult.error}</p>{/if}
    </section>

    <section>
      <h2>この PC の名前（deviceName）</h2>
      {#if view.deviceError}<p class="error">{view.deviceError}</p>{/if}
      <label>
        deviceName
        <input bind:value={deviceName} />
        <span class="hint">確認ダイアログで、どの PC が保存したセーブかを表すのに使います</span>
      </label>
      <div class="actions">
        <button
          disabled={saving}
          onclick={() => save("device", () => SettingsService.SaveDeviceName(deviceName), (r) => (deviceResult = r))}
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
  .error {
    color: #b3261e;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
  }
</style>
