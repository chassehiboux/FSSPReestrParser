package registry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMandatorySupplementAndIDPriority(t *testing.T) {
	for _, direct := range []bool{false, true} {
		opt := setup(t)
		opt.SkipCaseRegisters = true
		opt.SkipAnalytics = true
		fixture(t, opt.Source, SourceFields, [][]any{{"ИП", 45000, "Иванов Иван", 100, "Исходный взыскатель", "ИД-1", "Д-1", "ОСП", 44900, 44901, 45030, nil, nil, nil}})
		data := [][]any{{"ИВАНОВ ИВАН", "Д-1", 100, "02.02.1990"}}
		if direct {
			data = append(data, []any{"Иванов Иван", "ИД-1", 100, "01.01.1980"})
		}
		export := filepath.Join(opt.Exports, "Выгрузка.xlsx")
		fixture(t, export, []string{"ФИО", "Номер ИД", "Переданная на принудительное исполнение сумма", "д.р."}, data)
		if err := CheckExport(context.Background(), export, false); err != nil {
			t.Fatal("Требуется лишнее поле", err)
		}
		result, err := Run(context.Background(), opt, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantCase := 1
		year := "1990"
		if direct {
			wantCase = 0
			year = "1980"
		}
		if result.Found != 1 || result.FoundByCase != wantCase || len(result.Files) != 3 {
			t.Fatal("Неверный результат двух способов", result)
		}
		names, _ := Names(opt.Month, opt.Year)
		init := readRows(t, filepath.Join(result.Dir, names[0]))
		end := readRows(t, filepath.Join(result.Dir, names[1]))
		missing := readRows(t, filepath.Join(result.Dir, names[2]))
		if len(init) != 2 || len(end) != 2 || len(missing) != 1 || !strings.Contains(init[1][5], year) || init[1][8] != "ИД-1" || end[1][4] != "Исходный взыскатель" {
			t.Fatal("Нарушен состав объединённых реестров или приоритет ИД", init, end, missing)
		}
	}
}
