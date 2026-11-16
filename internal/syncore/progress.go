// 転送の進捗の通知。Push のアップロードと Pull のダウンロードの進捗を、直列にしたコールバックで知らせる。
package syncore

import (
	"context"
	"sync"
)

// Progress は、Push のアップロードか Pull のダウンロードの進捗。全体には、実際に PUT・GET する object だけを数え、
// 済んだ分には、転送に成功した object だけを数える。
type Progress struct {
	FilesDone  int
	FilesTotal int
	BytesDone  int64
	BytesTotal int64
}

// progressReporter は、コールバックを同時に 2 つ呼ばないようにロックで直列にし、済んだ分を数える。nil なら何もしない。
type progressReporter struct {
	mu sync.Mutex
	// ctx は、呼び出し側から渡された ctx。errgroup の ctx ではないので、ほかの転送の失敗では終わらない
	ctx    context.Context
	notify func(Progress)
	p      Progress
	failed bool
}

// startProgress は、転送を始める前に、全体の数を 1 回知らせる。ctx には errgroup の ctx ではなく、呼び出し側から
// 渡された ctx を渡す。ctx が終わっていれば知らせない。
func startProgress(ctx context.Context, notify func(Progress), files int, bytes int64) *progressReporter {
	if notify == nil {
		return nil
	}
	r := &progressReporter{ctx: ctx, notify: notify, p: Progress{FilesTotal: files, BytesTotal: bytes}}
	if ctx.Err() != nil {
		r.failed = true
		return r
	}
	notify(r.p)
	return r
}

// done は、object を 1 つ転送し終えたときに呼ぶ。fail の後と、呼び出し側がキャンセルした後は、数えも知らせもしない。
func (r *progressReporter) done(size int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed || r.ctx.Err() != nil {
		return
	}
	r.p.FilesDone++
	r.p.BytesDone += size
	r.notify(r.p)
}

// fail は、転送が失敗した goroutine が、エラーを返す前に呼ぶ。errgroup が自分の ctx を終わらせるのは、失敗した関数が
// 戻った後なので、errgroup の ctx を見て止めると、その間に成功したほかの転送が知らせてしまう。
func (r *progressReporter) fail() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = true
}
