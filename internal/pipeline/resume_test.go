package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
)

func TestCompleteRepeatNeedsConsentAndKeepsSingleFolders(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(out.Dir, out.Files[0]))
	r.Confirm = func(e Existing) bool {
		if e.Kind != "complete" {
			t.Error(e)
		}
		return false
	}
	if _, err = r.Run(context.Background(), req, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(out.Dir, out.Files[0]))
	if string(before) != string(after) {
		t.Fatal("Отмена изменила реестр")
	}
	for _, n := range f.calls {
		if n != 1 {
			t.Fatal("Скачивание после отмены")
		}
	}
	r.Confirm = func(e Existing) bool { return e.Kind == "complete" }
	out2, err := r.Run(context.Background(), req, nil)
	if err != nil || out2.Dir != out.Dir {
		t.Fatal(out2, err)
	}
	for _, n := range f.calls {
		if n != 2 {
			t.Fatal("Неполная повторная выгрузка", f.calls)
		}
	}
	if _, err := os.Stat(filepath.Join(out.Dir, "Исх.")); !os.IsNotExist(err) {
		t.Fatal("Выгрузка внутри Сверка")
	}
	if _, err := os.Stat(exportsDir(out.Dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(out.Dir), "Сверка (1)")); !os.IsNotExist(err) {
		t.Fatal("Создан второй комплект")
	}
}

func TestMissingExportRecoveredAcrossLocalProfiles(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := model.BuildJobs(req.Profile)
	if err = os.Remove(filepath.Join(exportsDir(out.Dir), jobs[0].Filename)); err != nil {
		t.Fatal(err)
	}
	r.Local = filepath.Join(t.TempDir(), "another-profile")
	r.Confirm = func(e Existing) bool {
		if e.Kind != "partial" || e.Ready != len(jobs)-1 {
			t.Error(e)
		}
		return true
	}
	out, err = r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 1)
	for _, j := range jobs {
		want := 1
		if j.ID == jobs[0].ID {
			want = 2
		}
		if f.calls[j.ID] != want {
			t.Fatal(f.calls)
		}
	}
}

func TestMissingRegisterRecreatedWithoutExport(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(out.Dir, out.Files[0])); err != nil {
		t.Fatal(err)
	}
	r.Confirm = func(e Existing) bool {
		if e.Kind != "registers" {
			t.Error(e)
		}
		return true
	}
	out, err = r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 1)
	for _, n := range f.calls {
		if n != 1 {
			t.Fatal(f.calls)
		}
	}
}

func TestFullRepeatFailureDoesNotUseOldExportsOrRemoveRegisters(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out.Dir, out.Files[0])
	before, _ := os.ReadFile(path)
	jobs, _ := model.BuildJobs(req.Profile)
	f.failID, f.failures = jobs[0].ID, 100
	r.Confirm = func(Existing) bool { return true }
	started := false
	_, err = r.Run(context.Background(), req, func(e Event) {
		if e.Stage != nil && e.Stage.Stage > 0 {
			started = true
		}
	})
	if err == nil || started {
		t.Fatal("Сверка на старом файле", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("Потерян прежний реестр")
	}
	f.failures = 0
	_, err = r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.ID != f.failID && f.calls[j.ID] != 2 {
			t.Fatal("Повторена успешная новая выгрузка", f.calls)
		}
	}
}
