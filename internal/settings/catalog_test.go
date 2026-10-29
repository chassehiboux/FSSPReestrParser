package settings

import (
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogAgeAndDepartmentIsolation(t *testing.T) {
	now := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	root := t.TempDir()
	local := t.TempDir()
	p := model.Profile{Departments: []model.Department{{BaseID: "a", ID: 39}, {BaseID: "b", ID: 39}, {BaseID: "a", ID: 40}}}
	field := []model.Field{{Key: "x", Label: "Поле", Searchable: true}}
	for _, doc := range []CatalogDocument{{Schema: 1, BaseID: "a", DepartmentID: 39, UpdatedAt: now.Add(-7 * 24 * time.Hour), Fields: field}, {Schema: 1, BaseID: "b", DepartmentID: 39, UpdatedAt: now.Add(-8 * 24 * time.Hour), Fields: field}} {
		if err := PublishCatalog(root, doc); err != nil {
			t.Fatal(err)
		}
	}
	catalog, state := LoadSharedCatalog(root, local, []model.Profile{p, p}, now)
	if len(catalog) != 2 || state.Total != 3 || state.Stale != 1 || state.Missing != 1 {
		t.Fatalf("%v %+v", catalog, state)
	}
	if _, err := ReadCatalogDocument(filepath.Join(root, "a", "39.json")); err != nil {
		t.Fatal(err)
	}
	if Stale(CatalogDocument{UpdatedAt: now.Add(-7 * 24 * time.Hour)}, now) {
		t.Fatal("Exactly seven days should remain valid")
	}
}
