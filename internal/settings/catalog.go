package settings

import (
	"encoding/json"
	"fmt"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"os"
	"path/filepath"
	"time"
)

type CatalogDocument struct {
	Schema       int           `json:"schema"`
	BaseID       string        `json:"base_id"`
	DepartmentID int           `json:"department_id"`
	UpdatedAt    time.Time     `json:"updated_at"`
	Fields       []model.Field `json:"fields"`
}
type CatalogState struct {
	Total, Missing, Stale int
	Oldest                time.Time
}

func (s CatalogState) Message() string {
	if s.Missing+s.Stale > 0 {
		return fmt.Sprintf("Кэш устарел — обновите его самостоятельно. Старше 7 дней: %d; отсутствует или недоступен: %d из %d.", s.Stale, s.Missing, s.Total)
	}
	return fmt.Sprintf("Кэш актуален для %d департаментов. Самое раннее обновление: %s.", s.Total, s.Oldest.Local().Format("02.01.2006 15:04"))
}
func CatalogRoot(sharedSettings string) string {
	return filepath.Join(filepath.Dir(sharedSettings), "catalogs")
}
func CatalogPath(root string, d model.Department) string {
	return filepath.Join(root, model.SafeName(d.BaseID), fmt.Sprintf("%d.json", d.ID))
}
func Stale(doc CatalogDocument, now time.Time) bool {
	return doc.UpdatedAt.IsZero() || now.Sub(doc.UpdatedAt) > 7*24*time.Hour
}
func PublishCatalog(root string, doc CatalogDocument) error {
	return AtomicJSON(CatalogPath(root, model.Department{BaseID: doc.BaseID, ID: doc.DepartmentID}), doc)
}
func ReadCatalogDocument(path string) (CatalogDocument, error) {
	var doc CatalogDocument
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &doc)
	}
	if err == nil && (doc.Schema != 1 || len(doc.Fields) == 0 || doc.UpdatedAt.IsZero()) {
		err = fmt.Errorf("Некорректный кэш полей")
	}
	return doc, err
}
func LoadSharedCatalog(root, local string, profiles []model.Profile, now time.Time) (model.Catalog, CatalogState) {
	fields := LoadCatalog(filepath.Join(local, "catalog.json"))
	meta := map[string]CatalogDocument{}
	if raw, err := os.ReadFile(filepath.Join(local, "catalog-meta.json")); err == nil {
		json.Unmarshal(raw, &meta)
	}
	if meta == nil {
		meta = map[string]CatalogDocument{}
	}
	state := CatalogState{}
	seen := map[string]bool{}
	for _, p := range profiles {
		for _, d := range p.Departments {
			key := model.Scope(d)
			if seen[key] {
				continue
			}
			seen[key] = true
			state.Total++
			doc, err := ReadCatalogDocument(CatalogPath(root, d))
			if err != nil || doc.BaseID != d.BaseID || doc.DepartmentID != d.ID {
				state.Missing++
				continue
			}
			if Stale(doc, now) {
				state.Stale++
			}
			if state.Oldest.IsZero() || doc.UpdatedAt.Before(state.Oldest) {
				state.Oldest = doc.UpdatedAt
			}
			if previous := meta[key]; !previous.UpdatedAt.After(doc.UpdatedAt) {
				fields[key] = doc.Fields
				meta[key] = doc
			}
		}
	}
	AtomicJSON(filepath.Join(local, "catalog.json"), fields)
	AtomicJSON(filepath.Join(local, "catalog-meta.json"), meta)
	return fields, state
}
