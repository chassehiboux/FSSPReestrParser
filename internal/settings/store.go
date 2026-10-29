package settings

import (
	"encoding/json"
	"fmt"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	Shared string
	Local  string
	mu     sync.Mutex
}

func Read(path string) (model.Settings, error) {
	var s model.Settings
	b, e := os.ReadFile(path)
	if e != nil {
		return s, e
	}
	if len(b) > 4*1024*1024 {
		return s, fmt.Errorf("Слишком большой файл настроек")
	}
	e = json.Unmarshal(b, &s)
	if e == nil {
		e = s.Validate()
	}
	return s, e
}
func AtomicJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".fssp-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func (s *Store) Load() (model.Settings, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, e := Read(s.Shared)
	if e == nil {
		if cacheErr := AtomicJSON(filepath.Join(s.Local, "settings.json"), value); cacheErr != nil {
			return value, "Настройки загружены; не удалось сохранить локальную копию: " + cacheErr.Error(), nil
		}
		return value, "", nil
	}
	cached, cacheErr := Read(filepath.Join(s.Local, "settings.json"))
	if cacheErr != nil {
		return cached, "", fmt.Errorf("Общие настройки недоступны, корректной локальной копии нет: %w", e)
	}
	return cached, "Общие настройки недоступны. Используется сохранённая версия. " + e.Error(), nil
}
func (s *Store) Publish(value model.Settings, expected int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := value.Validate(); e != nil {
		return e
	}
	// Блокировка находится только рядом с общими настройками, вне папки выгрузки.
	lock, e := os.OpenFile(s.Shared+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("Настройки сейчас сохраняются или нет доступа к папке: %w", e)
	}
	lock.WriteString(time.Now().Format(time.RFC3339))
	lock.Close()
	defer os.Remove(s.Shared + ".lock")
	current, e := Read(s.Shared)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e == nil && current.Revision != expected {
		return fmt.Errorf("Настройки изменились. Закройте админку и откройте её заново")
	}
	if os.IsNotExist(e) && expected != 0 {
		return fmt.Errorf("Общий файл исчез. Сохранение отменено")
	}
	value.Revision = expected + 1
	if e = AtomicJSON(s.Shared, value); e != nil {
		return e
	}
	return AtomicJSON(filepath.Join(s.Local, "settings.json"), value)
}
func LoadCatalog(path string) model.Catalog {
	result := model.Catalog{}
	b, e := os.ReadFile(path)
	if e == nil {
		json.Unmarshal(b, &result)
	}
	if result == nil {
		result = model.Catalog{}
	}
	return result
}
