package registry

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCreditorExcludedAndPreservedInOutput(t *testing.T) {
	opt := setup(t)
	fixture(t, opt.Source, SourceFields, [][]any{{"ИП", 45000, "Иванов Иван", 100, "АО ЭК Восток", "ИД", "дело", "ОСП", 44900, 44901, 45030, nil, nil, nil}, {"ИП", 45000, "Петров Пётр", 200, nil, "ИД-2", "дело-2", "ОСП", 44900, 44901, 45030, nil, nil, nil}})
	fixture(t, filepath.Join(opt.Exports, "Выгрузка.xlsx"), exportFields, [][]any{{"ИВАНОВ ИВАН", "ИД", 100, "Совсем другой взыскатель", "01.01.1980", "другое дело"}, {"Петров Пётр", "ИД-2", 200, "Взыскатель", "02.02.1981", "дело-2"}})
	result, err := Run(context.Background(), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Found != 2 {
		t.Fatal(result)
	}
	ending := readRows(t, filepath.Join(result.Dir, result.Files[1]))
	if ending[1][4] != "АО ЭК Восток" || ending[2][4] != "" {
		t.Fatal("Взыскатель исходника изменён", ending)
	}
}

func TestDifferentCreditorsDoNotHideBirthConflict(t *testing.T) {
	opt := setup(t)
	fixture(t, opt.Source, SourceFields, [][]any{{"ИП", 45000, "Иванов Иван", 100, "Первый", "ИД", "дело", "ОСП", 44900, 44901, 45030, nil, nil, nil}})
	fixture(t, filepath.Join(opt.Exports, "Выгрузка.xlsx"), exportFields, [][]any{{"Иванов Иван", "ИД", 100, "Первый", "01.01.1980", "дело"}, {"Иванов Иван", "ИД", 100, "Второй", "02.01.1980", "другое дело"}})
	result, err := Run(context.Background(), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Found != 0 || result.FoundByCase != 0 {
		t.Fatal("Выбрана одна из конфликтующих дат", result)
	}
}
