package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

// testCreatedAt は、createTestGame が書く created_at と updated_at。
var testCreatedAt = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

var testGameInfo = metasync.GameInfo{ID: testGameID, Title: "Game", CreatedAt: testCreatedAt, UpdatedAt: testCreatedAt}

func gameInfo(t *testing.T, s *Store) metasync.GameInfo {
	t.Helper()
	g, err := s.GameInfo(context.Background(), testGameID)
	if err != nil {
		t.Fatalf("GameInfo: %v", err)
	}
	return g
}

func TestReplaceGameInfo(t *testing.T) {
	ctx := context.Background()
	remote := metasync.GameInfo{
		ID: testGameID, Title: "Remote", Brand: "Brand",
		ImageSha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CreatedAt:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name      string
		updatedAt time.Time
		want      bool
	}{
		{"8-G3 store/ReplaceGameInfo は DB の updated_at が小さいときだけ書き換える", testCreatedAt.Add(time.Millisecond), true},
		{"8-G3 store/ReplaceGameInfo は DB の updated_at が等しければ書き換えない", testCreatedAt, false},
		{"8-G3 store/ReplaceGameInfo は DB の updated_at が大きければ書き換えない", testCreatedAt.Add(-time.Millisecond), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTemp(t)
			createTestGame(t, s)
			g := remote
			g.UpdatedAt = tt.updatedAt

			got, err := s.ReplaceGameInfo(ctx, g)
			if err != nil {
				t.Fatalf("ReplaceGameInfo: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReplaceGameInfo = %v, want %v", got, tt.want)
			}
			want := testGameInfo
			if tt.want {
				want = g
			}
			if diff := cmp.Diff(want, gameInfo(t, s)); diff != "" {
				t.Errorf("GameInfo (-want +got):\n%s", diff)
			}
		})
	}

	invalid := []struct {
		name string
		edit func(g *metasync.GameInfo)
	}{
		{"title が空", func(g *metasync.GameInfo) { g.Title = "" }},
		{"imageSha256 が大文字", func(g *metasync.GameInfo) {
			g.ImageSha256 = "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
		}},
		{"imageSha256 の長さが違う", func(g *metasync.GameInfo) { g.ImageSha256 = "0123" }},
		{"updatedAt の年が 4 桁を超える", func(g *metasync.GameInfo) { g.UpdatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"createdAt の年が負", func(g *metasync.GameInfo) { g.CreatedAt = time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, tt := range invalid {
		t.Run("store/ReplaceGameInfo は形の合わない値があれば何も書かない（"+tt.name+"）", func(t *testing.T) {
			s, _ := openTemp(t)
			createTestGame(t, s)
			g := remote
			g.UpdatedAt = testCreatedAt.Add(time.Hour)
			tt.edit(&g)

			if _, err := s.ReplaceGameInfo(ctx, g); err == nil {
				t.Error("ReplaceGameInfo がエラーを返さなかった")
			}
			if diff := cmp.Diff(testGameInfo, gameInfo(t, s)); diff != "" {
				t.Errorf("GameInfo (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEditGame(t *testing.T) {
	ctx := context.Background()
	str := func(s string) *string { return &s }

	tests := []struct {
		name string
		edit GameEdit
		now  time.Time
		want metasync.GameInfo
	}{
		{
			name: "store/EditGame は updated_at を今の時刻にし、ミリ秒に切り捨てる",
			edit: GameEdit{Title: str("New")},
			now:  testCreatedAt.Add(time.Hour + 1_234_567),
			want: metasync.GameInfo{ID: testGameID, Title: "New", CreatedAt: testCreatedAt, UpdatedAt: testCreatedAt.Add(time.Hour + time.Millisecond)},
		},
		{
			name: "store/EditGame は時計が戻っていても、updated_at を前の値より 1 ms 進める",
			edit: GameEdit{Title: str("New")},
			now:  testCreatedAt.Add(-time.Hour),
			want: metasync.GameInfo{ID: testGameID, Title: "New", CreatedAt: testCreatedAt, UpdatedAt: testCreatedAt.Add(time.Millisecond)},
		},
		{
			name: "store/EditGame は指定しなかった項目を変えない",
			edit: GameEdit{Brand: str("Brand")},
			now:  testCreatedAt.Add(time.Hour),
			want: metasync.GameInfo{ID: testGameID, Title: "Game", Brand: "Brand", CreatedAt: testCreatedAt, UpdatedAt: testCreatedAt.Add(time.Hour)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTemp(t)
			createTestGame(t, s)

			if err := s.EditGame(ctx, testGameID, tt.edit, tt.now); err != nil {
				t.Fatalf("EditGame: %v", err)
			}
			if diff := cmp.Diff(tt.want, gameInfo(t, s)); diff != "" {
				t.Errorf("GameInfo (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("store/EditGame は空の brand で brand を消す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		now := testCreatedAt.Add(time.Hour)
		if err := s.EditGame(ctx, testGameID, GameEdit{Brand: str("Brand")}, now); err != nil {
			t.Fatalf("EditGame: %v", err)
		}

		if err := s.EditGame(ctx, testGameID, GameEdit{Brand: str("")}, now.Add(time.Hour)); err != nil {
			t.Fatalf("EditGame: %v", err)
		}
		want := metasync.GameInfo{ID: testGameID, Title: "Game", CreatedAt: testCreatedAt, UpdatedAt: now.Add(time.Hour)}
		if diff := cmp.Diff(want, gameInfo(t, s)); diff != "" {
			t.Errorf("GameInfo (-want +got):\n%s", diff)
		}
	})

	t.Run("store/EditGame は空の title を書かない", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		if err := s.EditGame(ctx, testGameID, GameEdit{Title: str("")}, testCreatedAt.Add(time.Hour)); err == nil {
			t.Error("EditGame がエラーを返さなかった")
		}
		if diff := cmp.Diff(testGameInfo, gameInfo(t, s)); diff != "" {
			t.Errorf("GameInfo (-want +got):\n%s", diff)
		}
	})

	t.Run("store/EditGame は登録していないゲームをエラーにする", func(t *testing.T) {
		s, _ := openTemp(t)

		if err := s.EditGame(ctx, testGameID, GameEdit{Title: str("New")}, testCreatedAt); err == nil {
			t.Error("EditGame がエラーを返さなかった")
		}
	})
}
