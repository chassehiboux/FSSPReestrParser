// Package model описывает регионы и блоки выгрузки независимо от интерфейса и браузера.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type Field struct {
	Key         string   `json:"key"`
	FilterField string   `json:"filter_field"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Searchable  bool     `json:"searchable"`
	Options     []Option `json:"options"`
}
type Catalog map[string][]Field
type Base struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}
type Department struct {
	BaseID string `json:"base_id"`
	ID     int    `json:"id"`
	Name   string `json:"name"`
}
type Row struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}
type Block struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Linked  bool   `json:"linked"`
	Rows    []Row  `json:"rows"`
}
type Profile struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Bases       []Base       `json:"bases"`
	Departments []Department `json:"departments"`
	Blocks      []Block      `json:"blocks"`
}
type Settings struct {
	Schema   int       `json:"schema"`
	Revision int       `json:"revision"`
	Headless bool      `json:"headless"`
	Parallel int       `json:"parallel"`
	Profiles []Profile `json:"profiles"`
}
type Condition struct {
	Key   string
	Value string
	Label string
}
type Job struct {
	ReplaceExisting bool
	ID              string
	Block           string
	Base            Base
	Department      Department
	Conditions      []Condition
	Filename        string
}

var Statuses = []string{"Направлены на исполнение (нет возбуждения)", "В исполнении (возбуждены ИП)", "ИД возвращенные из ФССП", "Подготовка ИД к возврату в ПУ"}

const MaxParallel = 20

func Scope(d Department) string { return fmt.Sprintf("%s:%d", d.BaseID, d.ID) }
func Primary() Block {
	return Block{ID: "primary", Name: "Основной блок", Enabled: true, Rows: []Row{{Key: "CaseStatusName", Values: append([]string(nil), Statuses...)}}}
}
func (s Settings) Validate() error {
	if s.Schema != 1 || s.Revision < 0 || len(s.Profiles) == 0 {
		return fmt.Errorf("Некорректная схема, версия, параллельность или список регионов")
	}
	if s.Parallel < 1 || s.Parallel > MaxParallel {
		return fmt.Errorf("Число одновременно активных заданий должно быть от 1 до %d", MaxParallel)
	}
	profiles := map[string]bool{}
	for _, p := range s.Profiles {
		if p.ID == "" || profiles[p.ID] || strings.TrimSpace(p.Name) == "" || len(p.Departments) == 0 {
			return fmt.Errorf("Некорректный регион: %s", p.Name)
		}
		profiles[p.ID] = true
		bases := map[string]bool{}
		for _, b := range p.Bases {
			u, e := url.Parse(b.URL)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || b.ID == "" || bases[b.ID] {
				return fmt.Errorf("Некорректная база: %s", b.Name)
			}
			bases[b.ID] = true
		}
		deps := map[string]bool{}
		for _, d := range p.Departments {
			if !bases[d.BaseID] || d.ID <= 0 || strings.TrimSpace(d.Name) == "" || deps[Scope(d)] {
				return fmt.Errorf("Некорректный департамент: %s", d.Name)
			}
			deps[Scope(d)] = true
		}
		blocks := map[string]bool{"primary": true}
		for _, b := range p.Blocks {
			if b.ID == "" || blocks[b.ID] || strings.TrimSpace(b.Name) == "" {
				return fmt.Errorf("Некорректное имя или ID блока: %s", b.Name)
			}
			blocks[b.ID] = true
			if !b.Enabled {
				continue
			}
			if len(b.Rows) == 0 {
				return fmt.Errorf("Блок «%s»: добавьте поле", b.Name)
			}
			fields := map[string]bool{}
			for _, r := range b.Rows {
				if r.Key == "" || fields[r.Key] || len(r.Values) == 0 {
					return fmt.Errorf("Блок «%s»: выберите уникальные поля и значения", b.Name)
				}
				fields[r.Key] = true
				for _, v := range r.Values {
					if strings.TrimSpace(v) == "" {
						return fmt.Errorf("Блок «%s»: пустое значение", b.Name)
					}
				}
			}
		}
	}
	return nil
}

// BuildJobs разворачивает блоки в задания. Допустимость проверяется по живой форме перед запросом.
func BuildJobs(p Profile) ([]Job, error) {
	var jobs []Job
	seen := map[string]bool{}
	names := map[string]int{}
	blocks := append([]Block{Primary()}, p.Blocks...)
	for _, b := range blocks {
		if !b.Enabled {
			continue
		}
		var combinations [][]Condition
		if b.Linked {
			combinations = [][]Condition{{}}
		}
		for _, r := range b.Rows {
			var next [][]Condition
			values := map[string]bool{}
			for _, v := range r.Values {
				if values[v] {
					continue
				}
				values[v] = true
				c := Condition{Key: r.Key, Value: v, Label: v}
				if b.Linked {
					for _, prev := range combinations {
						copyPrev := append([]Condition(nil), prev...)
						next = append(next, append(copyPrev, c))
					}
				} else {
					next = append(next, []Condition{c})
				}
			}
			if b.Linked {
				combinations = next
			} else {
				combinations = append(combinations, next...)
			}
			if len(combinations) > 10000 {
				return nil, fmt.Errorf("Слишком много комбинаций в блоке «%s»", b.Name)
			}
		}
		for _, d := range p.Departments {
			var base Base
			for _, candidate := range p.Bases {
				if candidate.ID == d.BaseID {
					base = candidate
					break
				}
			}
			if base.URL == "" {
				return nil, fmt.Errorf("Не найдена база департамента %s", d.Name)
			}
			for _, cs := range combinations {
				if len(cs) == 0 {
					continue
				}
				identityConditions := map[string]string{}
				parts := []string{d.Name}
				for _, c := range cs {
					identityConditions[c.Key] = c.Value
					parts = append(parts, c.Label)
				}
				raw, _ := json.Marshal([]any{base.ID, d.ID, identityConditions})
				sum := sha256.Sum256(raw)
				id := hex.EncodeToString(sum[:])
				if seen[id] {
					continue
				}
				seen[id] = true
				name := SafeName(strings.Join(parts, " - "))
				names[strings.ToLower(name)]++
				if len(utf16.Encode([]rune(name))) > 240 {
					return nil, fmt.Errorf("Слишком длинное имя: %s. Сократите значения или число связанных полей", name)
				}
				jobs = append(jobs, Job{ID: id, Block: b.Name, Base: base, Department: d, Conditions: cs, Filename: name + ".xlsx"})
				if len(jobs) > 30000 {
					return nil, fmt.Errorf("Слишком много заданий; сократите выбор")
				}
			}
		}
	}
	for i := range jobs {
		stem := strings.TrimSuffix(jobs[i].Filename, ".xlsx")
		if names[strings.ToLower(stem)] > 1 {
			jobs[i].Filename = stem + " - " + jobs[i].ID[:8] + ".xlsx"
		}
	}
	return jobs, nil
}

func SafeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimRight(strings.TrimSpace(s), ". ")
	if s == "" {
		return "Без названия"
	}
	stem := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL"
	for i := 1; i <= 9; i++ {
		reserved = reserved || stem == fmt.Sprint("COM", i) || stem == fmt.Sprint("LPT", i)
	}
	if reserved {
		s = "_" + s
	}
	return s
}

func NewRunDir(downloads string, now time.Time) (string, error) {
	dir, _, err := DayDir(downloads, now)
	return dir, err
}
func DayDir(downloads string, now time.Time) (string, bool, error) {
	root := filepath.Join(downloads, "Выгрузка ОВЗИД для реестров ФССП")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", false, err
	}
	dir := filepath.Join(root, now.Format("2006.01.02"))
	err := os.Mkdir(dir, 0700)
	if err == nil {
		return dir, false, nil
	}
	if !os.IsExist(err) {
		return "", false, err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false, fmt.Errorf("Путь дневной выгрузки занят файлом")
	}
	return dir, true, nil
}
