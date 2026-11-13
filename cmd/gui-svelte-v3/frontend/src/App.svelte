<script lang="ts">
  // ホーム画面。一覧の中身・文言・並び順の選択肢は Go の HomeView が決め、ここは表示と入力の受け渡しだけをする。
  import { CancelError, type CancellablePromise } from '@wailsio/runtime'
  import {
    App,
    type ConfirmView,
    type GameCardView,
    type HomeView,
    type NewGameInput,
  } from '../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/gui-svelte-v3'
  import SyncDialog from './SyncDialog.svelte'

  let query = $state('')
  let sort = $state('title')
  let desc = $state(false)
  let view = $state<HomeView | null>(null)
  let drawer = $state(false)
  let confirm = $state<ConfirmView | null>(null)
  let toast = $state<{ kind: 'error' | 'info'; message: string } | null>(null)
  let addDialog: HTMLDialogElement
  let form = $state<NewGameInput>({ title: '', brand: '', saveFolderPath: '', exePath: '' })

  function notify(kind: 'error' | 'info', message: string) {
    toast = { kind, message }
    setTimeout(() => (toast = null), 3000)
  }

  // 前の読み込みを cancel() してから次を始める。Go の側の ctx も打ち切られ、
  // 古い結果が後から届いて表示を巻き戻すことがない
  let pending: CancellablePromise<HomeView> | null = null
  async function load(q: string, s: string, d: boolean) {
    pending?.cancel()
    const p = App.Home(q, s, d)
    pending = p
    try {
      view = await p
    } catch (e) {
      if (!(e instanceof CancelError)) notify('error', String(e))
    }
  }

  // 検索語の入力のたびに Go を呼ばないよう、300ms 待ってから読み直す
  $effect(() => {
    const q = query, s = sort, d = desc
    const t = setTimeout(() => load(q, s, d), 300)
    return () => clearTimeout(t)
  })

  async function addGame(e: SubmitEvent) {
    e.preventDefault()
    try {
      await App.AddGame(form)
      addDialog.close()
      form = { title: '', brand: '', saveFolderPath: '', exePath: '' }
      await load(query, sort, desc)
    } catch (err) {
      notify('error', String(err))
    }
  }

  async function launch(g: GameCardView) {
    confirm = await App.Launch(g.id, g.title)
  }
</script>

<div class="app">
  <header class="titlebar">
    <button class="btn btn-ghost menu-button" aria-label="メニューを開く" onclick={() => (drawer = true)}>☰</button>
    <h1>ホーム</h1>
  </header>
  <main>
    <div class="panel toolbar">
      <input type="search" class="input search" placeholder="ゲームタイトルやブランド名で検索..." bind:value={query} />
      <div class="sort">
        <span class="label">並び順</span>
        <select class="select" bind:value={sort}>
          {#each view?.sortOptions ?? [] as o (o.value)}
            <option value={o.value}>{o.label}</option>
          {/each}
        </select>
        <select class="select" bind:value={desc}>
          <option value={false}>昇順</option>
          <option value={true}>降順</option>
        </select>
      </div>
    </div>

    {#if view && view.games.length === 0}
      <div class="empty">
        <p class="title">ゲームが見つかりませんでした</p>
        <p class="hint">検索条件を変更するか、新しいゲームを追加してください</p>
      </div>
    {:else if view}
      <div class="grid">
        {#each view.games as g (g.id)}
          <div class="panel card">
            <div class="thumb">
              {g.initial}
              <button class="play" aria-label="ゲームを起動" onclick={() => launch(g)}>▶</button>
            </div>
            <div class="body">
              <h3>{g.title}</h3>
              <p>{g.brand}</p>
              <p>{g.playInfo}</p>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </main>
</div>

{#if drawer}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="drawer-overlay" onclick={() => (drawer = false)}></div>
{/if}
<aside class="sidebar" class:open={drawer}>
  <div class="brand">☁ CloudLaunch</div>
  <nav><a class="active" href="/">ホーム</a></nav>
  <nav class="bottom"><span class="disabled">設定（未実装）</span></nav>
</aside>

<button class="fab" aria-label="ゲームを追加" onclick={() => addDialog.showModal()}>+</button>
<dialog bind:this={addDialog}>
  <h2>ゲームを追加</h2>
  <form onsubmit={addGame}>
    <input class="input" placeholder="タイトル" required bind:value={form.title} />
    <input class="input" placeholder="ブランド（任意）" bind:value={form.brand} />
    <input class="input" placeholder="セーブフォルダ" required bind:value={form.saveFolderPath} />
    <input class="input" placeholder="実行ファイル" required bind:value={form.exePath} />
    <div class="actions">
      <button type="button" class="btn" onclick={() => addDialog.close()}>キャンセル</button>
      <button class="btn btn-primary">追加</button>
    </div>
  </form>
</dialog>

{#if confirm}
  <SyncDialog {confirm} onclose={() => (confirm = null)} onerror={(m) => notify('error', m)} />
{/if}

{#if toast}
  <div id="toast"><div class="alert" class:alert-error={toast.kind === 'error'} class:alert-info={toast.kind === 'info'}>{toast.message}</div></div>
{/if}
