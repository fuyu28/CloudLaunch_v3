<script lang="ts">
  // 確認ダイアログと、承認後の進捗。中身は Go の ConfirmView・ProgressView のとおりに表示する。
  import { Events } from '@wailsio/runtime'
  import { SyncProgress } from '../bindings/events'
  import {
    App,
    type ConfirmView,
    Direction,
    JobState,
    type ProgressView,
  } from '../bindings/github.com/fuyu28/CloudLaunch_v3/cmd/gui-svelte-v3'

  const { Approve, Cancel } = App

  let { confirm, onclose, onerror }: {
    confirm: ConfirmView
    onclose: () => void
    onerror: (message: string) => void
  } = $props()

  let dlg: HTMLDialogElement
  let choice = $state<Direction | ''>('')
  let progress = $state<ProgressView | null>(null)
  const conflict = $derived(confirm.direction === Direction.DirectionConflict)

  $effect(() => dlg.showModal())

  // e.data の型（ProgressView）は、Go の RegisterEvent から生成されている
  $effect(() =>
    Events.On(SyncProgress, (e) => {
      if (e.data.planId === confirm.planId) progress = e.data
    }),
  )

  async function approve(e: SubmitEvent) {
    e.preventDefault()
    try {
      const p = await Approve(confirm.planId, conflict ? (choice as Direction) : confirm.direction)
      progress ??= p // 先にイベントが届いていたら、そちらが新しい
    } catch (err) {
      onerror(String(err))
    }
  }

  function close() {
    dlg.close()
    onclose()
  }
</script>

<!-- Esc で閉じないのは、転送中に閉じると進捗もキャンセルの手段も見えなくなるため -->
<dialog bind:this={dlg} oncancel={(e) => e.preventDefault()}>
  {#if progress}
    <h2>{progress.heading}</h2>
    <progress max="100" value={progress.percent}></progress>
    <p class:muted={progress.state === JobState.JobRunning}>{progress.detail}</p>
    <div class="actions">
      {#if progress.state === JobState.JobRunning}
        <button class="btn" onclick={() => Cancel(confirm.planId)}>キャンセル</button>
      {:else}
        <button class="btn btn-primary" onclick={close}>閉じる</button>
      {/if}
    </div>
  {:else}
    <h2>{confirm.heading}</h2>
    <form onsubmit={approve}>
      {#if conflict}
        <p class="muted">この PC とクラウドの両方で、前回の同期のあとにセーブが変わりました。どちらで上書きするかを選んでください。</p>
        <div class="compare">
          {#each [{ s: confirm.local, d: Direction.DirectionUpload }, { s: confirm.remote, d: Direction.DirectionDownload }] as side (side.d)}
            <label class="side" class:selected={choice === side.d}>
              <input type="radio" name="choice" value={side.d} bind:group={choice} />
              <strong>{side.s.heading}</strong>
              {#each side.s.lines as line (line)}<span>{line}</span>{/each}
              <span class="hint">{side.s.hint}</span>
            </label>
          {/each}
        </div>
      {:else}
        <dl class="facts">
          {#each confirm.facts as f (f.label)}
            <dt>{f.label}</dt>
            <dd>{f.value}</dd>
          {/each}
        </dl>
      {/if}
      <div class="actions">
        <button type="button" class="btn" onclick={close}>{confirm.declineLabel}</button>
        <button class="btn btn-primary" disabled={conflict && !choice}>{confirm.approveLabel}</button>
      </div>
    </form>
  {/if}
</dialog>
