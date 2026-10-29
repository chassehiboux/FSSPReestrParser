package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *Engine) SetSharedCatalog(root string) {
	e.catalogMu.Lock()
	e.sharedCatalog = root
	e.catalogPending = map[string]settings.CatalogDocument{}
	e.catalogWake = make(chan struct{}, 1)
	e.catalogStop = make(chan struct{})
	e.catalogMu.Unlock()
	go e.catalogPublisher()
}
func (e *Engine) catalogPublisher() {
	for {
		select {
		case <-e.catalogStop:
			return
		case <-e.catalogWake:
		}
		for {
			e.catalogMu.Lock()
			var key string
			var doc settings.CatalogDocument
			for k, v := range e.catalogPending {
				key = k
				doc = v
				delete(e.catalogPending, k)
				break
			}
			if key == "" {
				e.catalogMu.Unlock()
				break
			}
			e.catalogWriting = true
			e.catalogMu.Unlock()
			err := settings.PublishCatalog(e.sharedCatalog, doc)
			e.catalogMu.Lock()
			e.catalogWriting = false
			if err != nil {
				e.catalogLastError = fmt.Errorf("Общий кэш %s: %w", key, err)
			}
			e.catalogMu.Unlock()
		}
	}
}
func (e *Engine) FlushCatalog(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		e.catalogMu.Lock()
		done := len(e.catalogPending) == 0 && !e.catalogWriting
		err := e.catalogLastError
		e.catalogMu.Unlock()
		if done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (e *Engine) rememberCatalog(base string, dep int, fields []model.Field) {
	doc := settings.CatalogDocument{Schema: 1, BaseID: base, DepartmentID: dep, UpdatedAt: time.Now().UTC(), Fields: fields}
	key := model.Scope(model.Department{BaseID: base, ID: dep})
	e.catalogMu.Lock()
	defer e.catalogMu.Unlock()
	e.catalog[key] = fields
	settings.AtomicJSON(filepath.Join(e.local, "catalog.json"), e.catalog)
	meta := map[string]settings.CatalogDocument{}
	if raw, err := os.ReadFile(filepath.Join(e.local, "catalog-meta.json")); err == nil {
		json.Unmarshal(raw, &meta)
	}
	if meta == nil {
		meta = map[string]settings.CatalogDocument{}
	}
	meta[key] = doc
	settings.AtomicJSON(filepath.Join(e.local, "catalog-meta.json"), meta)
	if e.sharedCatalog != "" {
		e.catalogPending[key] = doc
		select {
		case e.catalogWake <- struct{}{}:
		default:
		}
	}
}
func (e *Engine) RefreshAll(ctx context.Context, profiles []model.Profile) (model.Catalog, error) {
	var issues []string
	for _, p := range profiles {
		_, err := e.Refresh(ctx, p)
		if err != nil {
			issues = append(issues, err.Error())
		}
	}
	flushCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := e.FlushCatalog(flushCtx); err != nil {
		issues = append(issues, err.Error())
	}
	if len(issues) > 0 {
		return e.Catalog(), fmt.Errorf("%s", strings.Join(issues, "\n"))
	}
	return e.Catalog(), nil
}
