// GUI の試作で使う偽の同期。syncore の Status と Push・Pull の代わりに、確認ダイアログの中身と進捗を作る。
// 画面の作りを比べるための道具で、実際には何も転送しない。
package main

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Direction string

const (
	DirectionDownload Direction = "download"
	DirectionUpload   Direction = "upload"
	DirectionConflict Direction = "conflict"
)

type JobState string

const (
	JobRunning   JobState = "running"
	JobDone      JobState = "done"
	JobCancelled JobState = "cancelled"
)

type Snapshot struct {
	Device     string
	ModifiedAt time.Time
	Files      int
	Bytes      int64
}

type Plan struct {
	ID        string
	GameID    string
	GameTitle string
	Direction Direction
	Added     int
	Changed   int
	Deleted   int
	Bytes     int64
	Local     Snapshot
	Remote    Snapshot
}

type Progress struct {
	PlanID    string
	GameTitle string
	Direction Direction // 競合を承認した後は、選んだ方向が入る
	State     JobState
	DoneBytes int64
	Total     int64
}

var errUnknownPlan = errors.New("plan not found or already started")

type fakeSync struct {
	mu     sync.Mutex
	plans  map[string]Plan
	jobs   map[string]*job
	notify func(Progress) // 進捗が変わるたびに呼ぶ。直列には呼ばないので、受け手は並行に呼ばれてよいようにする
}

type job struct {
	progress Progress
	cancel   context.CancelFunc
}

func newFakeSync(notify func(Progress)) *fakeSync {
	return &fakeSync{plans: map[string]Plan{}, jobs: map[string]*job{}, notify: notify}
}

// Status は、ゲーム ID から決まる偽の判定を返す。同じゲームなら毎回同じ方向になる。
func (f *fakeSync) Status(gameID, title string) Plan {
	h := fnv.New32a()
	_, _ = h.Write([]byte(gameID))
	n := h.Sum32()
	dir := []Direction{DirectionDownload, DirectionConflict, DirectionUpload}[(n>>8)%3]
	now := time.Now()
	p := Plan{
		ID:        uuid.NewString(),
		GameID:    gameID,
		GameTitle: title,
		Direction: dir,
		Added:     int(n % 7),
		Changed:   int(n%5) + 1,
		Deleted:   int(n % 2),
		Bytes:     int64(n%40+10) << 20,
		Local:     Snapshot{Device: "この PC", ModifiedAt: now.Add(-26 * time.Hour), Files: 42, Bytes: 180 << 20},
		Remote:    Snapshot{Device: "DESKTOP-B", ModifiedAt: now.Add(-3 * time.Hour), Files: 45, Bytes: 196 << 20},
	}
	f.mu.Lock()
	f.plans[p.ID] = p
	f.mu.Unlock()
	return p
}

// Start は、承認された plan の転送を始めてすぐに返す。競合のときは choice（download / upload）で方向を選ぶ。
func (f *fakeSync) Start(planID string, choice Direction) (Progress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.plans[planID]
	if !ok {
		return Progress{}, errUnknownPlan
	}
	delete(f.plans, planID)
	dir := p.Direction
	if dir == DirectionConflict {
		if choice != DirectionDownload && choice != DirectionUpload {
			return Progress{}, errors.New("choose download or upload")
		}
		dir = choice
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		progress: Progress{PlanID: planID, GameTitle: p.GameTitle, Direction: dir, State: JobRunning, Total: p.Bytes},
		cancel:   cancel,
	}
	f.jobs[planID] = j
	go f.run(ctx, j)
	return j.progress, nil
}

func (f *fakeSync) run(ctx context.Context, j *job) {
	const steps = 40
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			f.update(j, func(p *Progress) { p.State = JobCancelled })
			return
		case <-t.C:
		}
		f.update(j, func(p *Progress) {
			p.DoneBytes = p.Total * int64(i) / steps
			if i >= steps {
				p.State = JobDone
			}
		})
		if i >= steps {
			return
		}
	}
}

func (f *fakeSync) update(j *job, fn func(*Progress)) {
	f.mu.Lock()
	fn(&j.progress)
	p := j.progress
	f.mu.Unlock()
	f.notify(p)
}

func (f *fakeSync) Cancel(planID string) {
	f.mu.Lock()
	j, ok := f.jobs[planID]
	f.mu.Unlock()
	if ok {
		j.cancel()
	}
}

func (f *fakeSync) Progress(planID string) (Progress, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[planID]
	if !ok {
		return Progress{}, false
	}
	return j.progress, true
}
