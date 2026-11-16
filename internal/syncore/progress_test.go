package syncore_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// progressLog は、進捗のコールバックが受けた値を、呼ばれた順に記録する。呼ばれている間に次の呼び出しが入ったら
// overlapped を立てる。
type progressLog struct {
	mu         sync.Mutex
	got        []syncore.Progress
	inCall     atomic.Bool
	overlapped atomic.Bool
	// hold は、1 回の呼び出しにかける時間。重なりを見つけやすくするために延ばす
	hold time.Duration
}

func (l *progressLog) record(p syncore.Progress) {
	if !l.inCall.CompareAndSwap(false, true) {
		l.overlapped.Store(true)
	}
	time.Sleep(l.hold)
	l.mu.Lock()
	l.got = append(l.got, p)
	l.mu.Unlock()
	l.inCall.Store(false)
}

func (l *progressLog) calls() []syncore.Progress {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]syncore.Progress(nil), l.got...)
}

// assertProgressed は、最初の 1 回で全体を知らせ、その後は 1 つずつ済んだ分が増えて、最後に全体と一致することを確かめる。
// 並列に転送するので、object ごとの BytesDone の増え方の順は確かめない。サイズ 0 の object では BytesDone は増えない。
func assertProgressed(t *testing.T, got []syncore.Progress, want syncore.Progress) {
	t.Helper()
	if len(got) != want.FilesTotal+1 {
		t.Fatalf("進捗を %d 回知らせた, want %d 回（最初の 1 回と object ごと）: %+v", len(got), want.FilesTotal+1, got)
	}
	if diff := cmp.Diff(syncore.Progress{FilesTotal: want.FilesTotal, BytesTotal: want.BytesTotal}, got[0]); diff != "" {
		t.Errorf("最初の進捗 (-want +got):\n%s", diff)
	}
	for i, p := range got[1:] {
		if p.FilesDone != i+1 || p.FilesTotal != want.FilesTotal || p.BytesTotal != want.BytesTotal || p.BytesDone < got[i].BytesDone {
			t.Errorf("%d 回目の進捗 = %+v, 前 = %+v, want FilesDone %d で BytesDone が減らない", i+2, p, got[i], i+1)
		}
	}
	if diff := cmp.Diff(want, got[len(got)-1]); diff != "" {
		t.Errorf("最後の進捗 (-want +got):\n%s", diff)
	}
}

func TestProgress(t *testing.T) {
	t.Run("5章/Push/リモートにない object だけを全体に数え、最初の 1 回と object ごとに進捗を知らせる", func(t *testing.T) {
		e := newEnv(t)
		// one はリモートにあり、same の 2 つは中身が同じなので、PUT するのは two と same の 2 つ
		e.putObject(t, objectKey("one"), "one")
		plan := approvedPush(t, e, map[string]string{"a.sav": "one", "b.sav": "two", "c.sav": "same", "d.sav": "same"})
		var log progressLog

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Progress: log.record}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		assertProgressed(t, log.calls(), syncore.Progress{FilesDone: 2, FilesTotal: 2, BytesDone: 7, BytesTotal: 7})
	})

	t.Run("5章/Push/転送するものがないときは、0/0 で 1 回だけ進捗を知らせる", func(t *testing.T) {
		e := newEnv(t)
		e.putObject(t, objectKey("one"), "one")
		plan := approvedPush(t, e, map[string]string{"a.sav": "one"})
		var log progressLog

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Progress: log.record}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if diff := cmp.Diff([]syncore.Progress{{}}, log.calls()); diff != "" {
			t.Errorf("進捗 (-want +got):\n%s", diff)
		}
	})

	t.Run("6章/Pull/取得する object を全体に数え、最初の 1 回と object ごとに進捗を知らせる", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, map[string]string{"a.sav": "one", "dir/b.sav": "three"})
		var log progressLog

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{Progress: log.record}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		assertProgressed(t, log.calls(), syncore.Progress{FilesDone: 2, FilesTotal: 2, BytesDone: 8, BytesTotal: 8})
	})

	t.Run("5章/Push/PUT が失敗したら、その後は進捗を知らせない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, map[string]string{"a.sav": "one", "b.sav": "two"})
		injected := errors.New("injected")
		// two の PUT に入ってから one の PUT を失敗させ、two の PUT は one が失敗した後に成功させる。two が PUT に
		// 入る前に one が失敗すると、two は errgroup のキャンセルで PUT まで進まず、失敗の後の成功が起きない
		twoStarted := make(chan struct{})
		failed := make(chan struct{})
		e.remote.Before = func(op memstore.Op, key string) error {
			switch {
			case op == memstore.OpPut && key == objectKey("one"):
				<-twoStarted
				close(failed)
				return injected
			case op == memstore.OpPut && key == objectKey("two"):
				close(twoStarted)
				<-failed
				// one の転送が失敗を記録してから、two を成功させる
				time.Sleep(50 * time.Millisecond)
			}
			return nil
		}
		var log progressLog

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Progress: log.record}); !errors.Is(err, injected) {
			t.Fatalf("Push error = %v, want injected", err)
		}
		if _, ok := e.getObject(t, objectKey("two")); !ok {
			t.Fatal("two の PUT が成功しなかった。失敗の後の成功を確かめられない")
		}
		// 失敗した one も、失敗の後に成功した two も数えず、最初の 1 回のほかは知らせない
		if diff := cmp.Diff([]syncore.Progress{{FilesTotal: 2, BytesTotal: 6}}, log.calls()); diff != "" {
			t.Errorf("進捗 (-want +got):\n%s", diff)
		}
	})

	t.Run("6章/Pull/キャンセルした後は、成功した転送も進捗を知らせない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, map[string]string{"a.sav": "one"})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// GET が始まった後に呼び出し側がキャンセルし、GET そのものは成功させる
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet && key == objectKey("one") {
				cancel()
			}
			return nil
		}
		var log progressLog

		if err := e.core.Pull(ctx, plan, syncore.PullOptions{Progress: log.record}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Pull error = %v, want context.Canceled", err)
		}
		if diff := cmp.Diff([]syncore.Progress{{FilesTotal: 1, BytesTotal: 3}}, log.calls()); diff != "" {
			t.Errorf("進捗 (-want +got):\n%s", diff)
		}
	})

	t.Run("7章/上書き/local と remote が同じときは、0/0 で 1 回だけ進捗を知らせる", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"a.sav": "one"}
		e.putRemoteFiles(t, files)
		writeFiles(t, e.saveDir, files)
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if !plan.SameContent() {
			t.Fatalf("plan = %+v, want local と remote が同じ", plan)
		}
		for _, tt := range []struct {
			name string
			run  func(progress func(syncore.Progress)) error
		}{
			{"Push", func(p func(syncore.Progress)) error {
				return e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true, Progress: p})
			}},
			{"Pull", func(p func(syncore.Progress)) error {
				return e.core.Pull(t.Context(), plan, syncore.PullOptions{Overwrite: true, Progress: p})
			}},
		} {
			var log progressLog
			if err := tt.run(log.record); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if diff := cmp.Diff([]syncore.Progress{{}}, log.calls()); diff != "" {
				t.Errorf("%s の進捗 (-want +got):\n%s", tt.name, diff)
			}
		}
	})

	t.Run("5章/Push/進捗のコールバックを同時に 2 つ呼ばない", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{}
		for i := range 32 {
			files[fmt.Sprintf("slot%02d.sav", i)] = fmt.Sprintf("content %02d", i)
		}
		plan := approvedPush(t, e, files)
		log := progressLog{hold: time.Millisecond}

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Progress: log.record}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if log.overlapped.Load() {
			t.Error("コールバックを同時に 2 つ呼んだ")
		}
		if got := len(log.calls()); got != len(files)+1 {
			t.Errorf("進捗を %d 回知らせた, want %d 回", got, len(files)+1)
		}
	})
}
