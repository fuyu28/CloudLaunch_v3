package syncore_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// overwriteSetup は、上書きを試す出発点の状態。nil の項目は「なし」を表す。
type overwriteSetup struct {
	base   map[string]string
	local  map[string]string
	remote map[string]string
}

// planFor は、setup の状態を作って Status を呼び、判定が want の plan を返す。remote は、その object と世代を
// testGeneration として置く。
func planFor(t *testing.T, e env, setup overwriteSetup, want syncore.Decision) syncore.Plan {
	t.Helper()
	if setup.base != nil {
		e.setBase(t, syncore.ManifestHash(entriesOf(setup.base)))
	}
	if setup.local == nil {
		removeSaveDir(t, e)
	} else {
		writeFiles(t, e.saveDir, setup.local)
	}
	if setup.remote != nil {
		for _, content := range setup.remote {
			e.putObject(t, objectKey(content), content)
		}
		e.putGeneration(t, entriesOf(setup.remote))
	}
	plan, err := e.status(t)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if plan.Decision != want {
		t.Fatalf("Decision = %v, want %v", plan.Decision, want)
	}
	return plan
}

var (
	owBase   = map[string]string{"slot.sav": "base"}
	owLocal  = map[string]string{"slot.sav": "local"}
	owRemote = map[string]string{"slot.sav": "remote"}
)

func TestOverwriteUpload(t *testing.T) {
	tests := []struct {
		name     string
		setup    overwriteSetup
		decision syncore.Decision
	}{
		{
			name:     "7-O1 判定が Push のときにアップロードで上書きすると、ふつうの Push と同じく HEAD と base を更新する",
			setup:    overwriteSetup{base: owBase, local: owLocal, remote: owBase},
			decision: syncore.DecisionPush,
		},
		{
			name:     "7-O1 判定が Pull のときにアップロードで上書きすると、リモートの新しい世代を local で置き換える",
			setup:    overwriteSetup{base: owBase, local: owBase, remote: owRemote},
			decision: syncore.DecisionPull,
		},
		{
			name:     "7-O1 競合のときにアップロードで上書きすると、local を HEAD にして base を更新する",
			setup:    overwriteSetup{base: owBase, local: owLocal, remote: owRemote},
			decision: syncore.DecisionConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			plan := planFor(t, e, tt.setup, tt.decision)

			if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); err != nil {
				t.Fatalf("Push: %v", err)
			}
			if head, _ := e.getObject(t, headKey()); head != pushGeneration {
				t.Errorf("HEAD = %q, want %q", head, pushGeneration)
			}
			body, ok := e.getObject(t, manifestKey(pushGeneration))
			if !ok {
				t.Fatal("manifest がない")
			}
			var m syncore.Manifest
			if err := json.Unmarshal([]byte(body), &m); err != nil {
				t.Fatalf("manifest を読めない: %v", err)
			}
			if diff := cmp.Diff(entriesOf(tt.setup.local), m.Entries); diff != "" {
				t.Errorf("manifest の entries (-want +got):\n%s", diff)
			}
			for _, content := range tt.setup.local {
				if got, _ := e.getObject(t, objectKey(content)); got != content {
					t.Errorf("object の中身 = %q, want %q", got, content)
				}
			}
			if got, want := e.base(t), syncore.ManifestHash(entriesOf(tt.setup.local)); got != want {
				t.Errorf("base = %q, want %q", got, want)
			}
		})
	}
}

func TestOverwriteDownload(t *testing.T) {
	tests := []struct {
		name     string
		setup    overwriteSetup
		decision syncore.Decision
	}{
		{
			name:     "7-O2 判定が Push のときにダウンロードで上書きすると、live を remote に入れ替え、元の live をバックアップに移す",
			setup:    overwriteSetup{base: owBase, local: owLocal, remote: owBase},
			decision: syncore.DecisionPush,
		},
		{
			name:     "7-O2 判定が Pull のときにダウンロードで上書きすると、ふつうの Pull と同じく live と base を更新する",
			setup:    overwriteSetup{base: owBase, local: owBase, remote: owRemote},
			decision: syncore.DecisionPull,
		},
		{
			name:     "7-O2 競合のときにダウンロードで上書きすると、live を remote に入れ替えて base を更新する",
			setup:    overwriteSetup{base: owBase, local: owLocal, remote: owRemote},
			decision: syncore.DecisionConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			plan := planFor(t, e, tt.setup, tt.decision)

			if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{Overwrite: true}); err != nil {
				t.Fatalf("Pull: %v", err)
			}
			if diff := cmp.Diff(tt.setup.remote, readFiles(t, e.saveDir)); diff != "" {
				t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
			}
			if got, want := e.base(t), syncore.ManifestHash(entriesOf(tt.setup.remote)); got != want {
				t.Errorf("base = %q, want %q", got, want)
			}
			backups := e.gameBackups(t)
			if len(backups) != 1 {
				t.Fatalf("バックアップ = %v, want 1 個", backups)
			}
			if diff := cmp.Diff(tt.setup.local, readFiles(t, filepath.Join(e.backupsDir, testGameID, backups[0]))); diff != "" {
				t.Errorf("バックアップの中身 (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOverwrite拒否と変更なし(t *testing.T) {
	t.Run("7-O3 local がなければ、アップロードで上書きを ErrLocalMissing で拒否し、何も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := planFor(t, e, overwriteSetup{base: owBase, remote: owBase}, syncore.DecisionConflict)
		before := e.keys(t, "games/")

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); !errors.Is(err, syncore.ErrLocalMissing) {
			t.Fatalf("Push error = %v, want ErrLocalMissing", err)
		}
		if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
			t.Errorf("リモートのキー (-want +got):\n%s", diff)
		}
		if head, _ := e.getObject(t, headKey()); head != testGeneration {
			t.Errorf("HEAD = %q, want %q", head, testGeneration)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(owBase)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("7-O4 remote がなければ、ダウンロードで上書きを ErrRemoteMissing で拒否し、何も変えない", func(t *testing.T) {
		e := newEnv(t)
		plan := planFor(t, e, overwriteSetup{local: owLocal}, syncore.DecisionPush)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{Overwrite: true}); !errors.Is(err, syncore.ErrRemoteMissing) {
			t.Fatalf("Pull error = %v, want ErrRemoteMissing", err)
		}
		assertAborted(t, e, owLocal)
		if backups := e.gameBackups(t); len(backups) != 0 {
			t.Errorf("バックアップができた: %v", backups)
		}
	})

	t.Run("7-O5 local と remote が同じなら、アップロードで上書きしても世代を作らず、base を記録する", func(t *testing.T) {
		e := newEnv(t)
		plan := planFor(t, e, overwriteSetup{local: owLocal, remote: owLocal}, syncore.DecisionNoChange)
		before := e.keys(t, "games/")

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
			t.Errorf("リモートのキー (-want +got):\n%s", diff)
		}
		if head, _ := e.getObject(t, headKey()); head != testGeneration {
			t.Errorf("HEAD = %q, want %q", head, testGeneration)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(owLocal)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("7-O5 local と remote が同じなら、ダウンロードで上書きしても live を入れ替えず、base を記録する", func(t *testing.T) {
		e := newEnv(t)
		plan := planFor(t, e, overwriteSetup{local: owLocal, remote: owLocal}, syncore.DecisionNoChange)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{Overwrite: true}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(owLocal, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if backups := e.gameBackups(t); len(backups) != 0 {
			t.Errorf("バックアップができた: %v", backups)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(owLocal)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("7章/上書き/上書きでも、承認した後に HEAD が変わっていれば Push は ErrPlanStale で止まる", func(t *testing.T) {
		e := newEnv(t)
		plan := planFor(t, e, overwriteSetup{base: owBase, local: owLocal, remote: owRemote}, syncore.DecisionConflict)
		e.putManifest(t, otherGeneration, entriesOf(owBase))
		e.putObject(t, headKey(), otherGeneration)

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Push error = %v, want ErrPlanStale", err)
		}
		if head, _ := e.getObject(t, headKey()); head != otherGeneration {
			t.Errorf("HEAD = %q, want %q", head, otherGeneration)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(owBase)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("7章/上書き/上書きでも、承認した後に HEAD が変わっていれば Pull は ErrPlanStale で止まる", func(t *testing.T) {
		e := newEnv(t)
		plan := planFor(t, e, overwriteSetup{base: owBase, local: owLocal, remote: owBase}, syncore.DecisionPush)
		e.putManifest(t, otherGeneration, entriesOf(owBase))
		e.putObject(t, headKey(), otherGeneration)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{Overwrite: true}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want ErrPlanStale", err)
		}
		if diff := cmp.Diff(owLocal, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if journal := e.journalCount(t); journal != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", journal)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(owBase)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})
}
