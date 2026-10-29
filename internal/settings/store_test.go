package settings

import (
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"os"
	"path/filepath"
	"testing"
)

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
