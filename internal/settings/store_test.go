package settings

import (
	"bytes"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestPersonalSettingsIsolationAndReset(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Shared: filepath.Join(dir, "settings.json"), Local: filepath.Join(dir, "local")}
	common := model.Settings{Schema: 1, Parallel: 3, Profiles: []model.Profile{{ID: "a", Name: "Регион", Bases: []model.Base{{ID: "b", URL: "https://example.test"}}, Departments: []model.Department{{BaseID: "b", ID: 1, Name: "Отдел"}}}}}
	if err := s.Publish(common, 0); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.Shared)
	cacheBefore, _ := os.ReadFile(filepath.Join(s.Local, "settings.json"))
	personal := common
	personal.Parallel, personal.SkipAnalytics, personal.SkipCaseRegisters = 20, true, true
	if err := s.SavePersonal(personal); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.Shared)
	cacheAfter, _ := os.ReadFile(filepath.Join(s.Local, "settings.json"))
	if !bytes.Equal(before, after) || !bytes.Equal(cacheBefore, cacheAfter) {
		t.Fatal("личное сохранение изменило общие настройки")
	}
	got, _, err := s.Load()
	if err != nil || got.Parallel != 20 || !got.SkipAnalytics || got.Revision != 1 || !s.PersonalActive() {
		t.Fatalf("личные: %+v %v", got, err)
	}
	got, _, err = s.UseCommon()
	if err != nil || got.Parallel != 3 || got.SkipAnalytics || s.PersonalActive() {
		t.Fatalf("общие: %+v %v", got, err)
	}
	if err = s.SavePersonal(personal); err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(personal, 1); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Load()
	if err != nil || got.Revision != 2 || !got.SkipCaseRegisters || s.PersonalActive() {
		t.Fatalf("публикация: %+v %v", got, err)
	}
	if err = s.SavePersonal(common); err != nil {
		t.Fatal(err)
	}
	os.Remove(s.Shared)
	os.Remove(filepath.Join(s.Local, "settings.json"))
	got, warning, err := s.Load()
	if err != nil || warning == "" || got.Parallel != 3 {
		t.Fatalf("личные без сети: %+v %s %v", got, warning, err)
	}
}

func TestPublishCacheAndConflictingRevision(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Shared: filepath.Join(dir, "shared/settings.json"), Local: filepath.Join(dir, "local")}
	v := model.Settings{Schema: 1, Parallel: 3, Headless: true, Profiles: []model.Profile{{ID: "a", Name: "Регион", Bases: []model.Base{{ID: "b", URL: "https://example.test"}}, Departments: []model.Department{{BaseID: "b", ID: 1, Name: "Департамент"}}}}}
	if e := os.MkdirAll(filepath.Dir(s.Shared), 0700); e != nil {
		t.Fatal(e)
	}
	if e := s.Publish(v, 0); e != nil {
		t.Fatal(e)
	}
	loaded, warning, e := s.Load()
	if e != nil || warning != "" || loaded.Revision != 1 {
		t.Fatalf("%+v %s %v", loaded, warning, e)
	}
	if e = s.Publish(v, 0); e == nil {
		t.Fatal("revision conflict accepted")
	}
	os.WriteFile(s.Shared, []byte("invalid json"), 0600)
	loaded, warning, e = s.Load()
	if e != nil || warning == "" || loaded.Revision != 1 {
		t.Fatalf("cache: %s %v", warning, e)
	}
}
