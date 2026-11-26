<script lang="ts">
  import { GameService } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";
  import type {
    FieldCheck,
    FormCheck,
    GameForm,
    LegacyView,
    LinkView,
  } from "../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/cloudlaunch-gui";

  let { onBack }: { onBack: (registered: boolean) => void } = $props();

  type PathField = "saveFolderPath" | "exePath" | "imagePath";
  type CheckField = keyof FormCheck;

  let form = $state<GameForm>({ title: "", brand: "", saveFolderPath: "", exePath: "", imagePath: "" });
  let check = $state<FormCheck | null>(null);
  // Go はフォーム全体を検査するので、まだ触っていない空の欄に「入れてください」を出さないよう、触った欄の結果だけを出す
  let touched = $state<Partial<Record<CheckField, boolean>>>({});
  let checkError = $state("");
  let error = $state("");
  let busy = $state(false);
  // checkSeq は、検査を始めた順の番号。後から始めた検査や登録の後に届いた古い応答で、欄の文を戻さないために使う。
  // 表示に使わないので $state にしない
  let checkSeq = 0;

  let gameId = $state("");
  let warning = $state("");
  let legacy = $state<LegacyView | null>(null);
  let selectedLegacy = $state("");
  let linkResult = $state<LinkView | null>(null);
  // 取り込みに失敗しても、選び直すか取り込まずに済ませられるよう、選ぶ表示を残す
  let choosingLegacy = $derived(!!legacy && !linkResult?.status);

  // runCheck と pick は、登録の間は何もしない。番号を進めると、登録の応答の欄ごとのエラーを古い応答として捨ててしまう。
  // 欄を無効にするだけでは、フォーカスのある欄が無効になったときの blur で呼ばれうるので、ここでも止める
  async function runCheck(field: CheckField) {
    if (busy) return;
    touched[field] = true;
    const seq = ++checkSeq;
    try {
      const c = await GameService.CheckForm($state.snapshot(form));
      if (seq !== checkSeq) return;
      check = c;
      checkError = "";
    } catch (e) {
      if (seq === checkSeq) checkError = "入力を確かめられませんでした: " + String(e);
    }
  }

  async function pick(field: PathField, checkField: CheckField, call: (current: string) => Promise<string>) {
    if (busy) return;
    try {
      const path = await call(form[field]);
      checkError = "";
      // ダイアログを開いている間に登録を押されたら、選んだパスで登録の後の欄を書き換えない
      if (!path || busy) return;
      form[field] = path;
      runCheck(checkField);
    } catch (e) {
      checkError = "選べませんでした: " + String(e);
    }
  }

  async function register() {
    busy = true;
    error = "";
    checkError = "";
    touched = { title: true, saveFolder: true, exe: true, image: true };
    const seq = ++checkSeq;
    try {
      const v = await GameService.Register($state.snapshot(form));
      if (seq === checkSeq) check = v.check;
      if (!v.gameId) {
        error = v.error;
        return;
      }
      if (!v.legacy && !v.warning) {
        onBack(true);
        return;
      }
      gameId = v.gameId;
      warning = v.warning;
      legacy = v.legacy;
      selectedLegacy = v.legacy?.suggested?.id ?? "";
    } catch (e) {
      error = String(e);
    } finally {
      busy = false;
    }
  }

  async function link() {
    busy = true;
    try {
      linkResult = await GameService.LinkLegacy(gameId, selectedLegacy);
    } catch (e) {
      linkResult = { status: "", error: String(e) };
    } finally {
      busy = false;
    }
  }
</script>

{#snippet fieldNote(field: CheckField)}
  {@const c: FieldCheck | undefined = touched[field] ? check?.[field] : undefined}
  {#if c?.error}<span class="error">{c.error}</span>{/if}
  {#if c?.warning}<span class="warning">{c.warning}</span>{/if}
{/snippet}

<main>
  <div class="top">
    <h1>ゲームを登録</h1>
    <!-- 登録した後は、旧履歴を取り込むかを選ぶまで戻らせない。戻ると 9-K1 の提案を見ないまま終わるため -->
    <button disabled={busy || choosingLegacy} onclick={() => onBack(!!gameId)}>一覧へ戻る</button>
  </div>

  {#if !gameId}
    <section>
      <fieldset disabled={busy}>
      <label>
        タイトル
        <input bind:value={form.title} onblur={() => runCheck("title")} />
        {@render fieldNote("title")}
      </label>
      <label>
        ブランド（任意）
        <input bind:value={form.brand} />
      </label>
      <label>
        セーブフォルダ
        <span class="path">
          <input bind:value={form.saveFolderPath} onblur={() => runCheck("saveFolder")} />
          <button onclick={() => pick("saveFolderPath", "saveFolder", GameService.PickSaveFolder)}>参照…</button>
        </span>
        {@render fieldNote("saveFolder")}
      </label>
      <label>
        実行ファイル
        <span class="path">
          <input bind:value={form.exePath} onblur={() => runCheck("exe")} />
          <button onclick={() => pick("exePath", "exe", GameService.PickExe)}>参照…</button>
        </span>
        <span class="hint">
          ランチャーではなく、ゲーム本体の exe を選んでください。ランチャーを選ぶと、実行中かどうかを見分けられず、遊んでいる間のセーブを同期してしまうことがあります
        </span>
        {@render fieldNote("exe")}
      </label>
      <label>
        画像（任意）
        <span class="path">
          <input bind:value={form.imagePath} onblur={() => runCheck("image")} />
          <button onclick={() => pick("imagePath", "image", GameService.PickImage)}>参照…</button>
        </span>
        <span class="hint">PNG・JPEG・WebP で、10 MiB まで</span>
        {@render fieldNote("image")}
      </label>
      </fieldset>
      <div class="actions">
        <button class="primary" disabled={busy} onclick={register}>{busy ? "登録中…" : "登録"}</button>
      </div>
      {#if checkError}<p class="error">{checkError}</p>{/if}
      {#if error}<p class="error">{error}</p>{/if}
    </section>
  {:else}
    <section>
      <p class="status">登録しました</p>
      {#if warning}<p class="error">{warning}</p>{/if}
      {#if choosingLegacy && legacy}
        <h2>旧版のプレイ履歴の取り込み</h2>
        {#if legacy.suggested}
          <p>旧版のプレイ履歴に、タイトルが一致するゲームがあります。取り込みますか？</p>
        {:else}
          <p>旧版のプレイ履歴に、タイトルが一致するゲームがないか、複数あります。取り込むゲームを選ぶか、取り込まずに済ませてください。</p>
        {/if}
        <ul class="legacy">
          {#each [...(legacy.suggested ? [legacy.suggested] : []), ...legacy.candidates] as g (g.id)}
            <li>
              <label class="choice">
                <input type="radio" name="legacy" value={g.id} bind:group={selectedLegacy} />
                <span>
                  <span class="title">{g.title}</span>
                  {#if g.id === legacy.suggested?.id}<span class="badge">一致</span>{/if}
                  <span class="hint">{g.summary}（旧版の gameId: {g.id}）</span>
                </span>
              </label>
            </li>
          {/each}
        </ul>
        <div class="actions">
          <button disabled={busy} onclick={() => onBack(true)}>取り込まない</button>
          <button class="primary" disabled={busy || !selectedLegacy} onclick={link}>取り込む</button>
        </div>
      {/if}
      {#if linkResult?.status}<p class="status">{linkResult.status}</p>{/if}
      {#if linkResult?.error}<p class="error">{linkResult.error}</p>{/if}
    </section>
  {/if}
</main>

<style>
  main {
    padding: 16px 24px 80px;
    max-width: 720px;
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  section {
    display: grid;
    gap: 12px;
    padding: 12px 16px;
    background: #fff;
    border: 1px solid #e0e0e3;
    border-radius: 8px;
  }
  h2 {
    margin: 0;
    font-size: 1.05em;
  }
  fieldset {
    display: grid;
    gap: 12px;
    margin: 0;
    padding: 0;
    border: none;
  }
  label {
    display: grid;
    gap: 4px;
  }
  input {
    padding: 4px 6px;
  }
  .path {
    display: flex;
    gap: 8px;
  }
  .path input {
    flex: 1;
  }
  .hint,
  .status {
    font-size: 0.875em;
    color: #6e6e73;
  }
  .error {
    color: #b3261e;
  }
  .warning {
    color: #8a5a00;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .primary {
    font-weight: 600;
  }
  .legacy {
    list-style: none;
    padding: 0;
    margin: 0;
    display: grid;
    gap: 8px;
  }
  .choice {
    display: flex;
    align-items: flex-start;
    gap: 8px;
  }
  .choice > span {
    display: grid;
    gap: 2px;
  }
  .title {
    font-weight: 600;
  }
  .badge {
    font-size: 0.75em;
    color: #1d5fbf;
  }
</style>
