package registry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestExactAndSupplementaryEmptyBirthGoIntoMainRegisters(t *testing.T) {
	opt := setup(t)
	fixture(t, opt.Source, SourceFields, [][]any{{"ИП-1", 45000, "Первый Иван", 100, "АО", "ИД-1", "Д-1", "ОСП", 44900, 44901, 45030, nil, nil, nil}, {"ИП-2", 45000, "Второй Иван", 200, "АО", "иной ИД", "ИД-2", "ОСП", 44900, 44901, 45030, nil, nil, nil}})
	fixture(t, filepath.Join(opt.Exports, "Выгрузка.xlsx"), exportFields, [][]any{{"Первый Иван", "ИД-1", 100, "АО", nil, "Д-1"}, {"Второй Иван", "ИД-2", 200, "АО", nil, "Д-2"}})
	result, err := Run(context.Background(), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Found != 2 || result.WithoutBirth != 2 || result.FoundByCase != 1 {
		t.Fatal("Совпадения с пустой ДР потеряны", result)
	}
	main := readRows(t, filepath.Join(result.Dir, result.Files[0]))
	ending := readRows(t, filepath.Join(result.Dir, result.Files[1]))
	missing := readRows(t, filepath.Join(result.Dir, result.Files[2]))
	if len(main) != 3 || len(ending) != 3 || len(missing) != 1 {
		t.Fatal("Неверное распределение строк", main, ending, missing)
	}
	for _, r := range main[1:] {
		if r[5] != "" {
			t.Fatal("Дата рождения выдумана", r)
		}
	}
	analytics := readRows(t, filepath.Join(result.Dir, result.Files[3]))
	if !strings.Contains(analytics[1][15], "отсутствует") || !strings.Contains(analytics[2][15], "отсутствует") {
		t.Fatal("Отсутствие ДР не отмечено")
	}
	summary := BirthSummary(result.Found, result.FoundByCase, false, result.WithoutBirth)
	if !strings.Contains(summary, "ДР заполнена: 0") || !strings.Contains(summary, "пустая ДР: 2") {
		t.Fatal("Счётчик обещает несуществующую ДР", summary)
	}
}
