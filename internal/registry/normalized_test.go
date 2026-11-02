package registry

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDecimalToleranceIsStrictlyBelowOneKopeck(t *testing.T) {
	for _, tc := range []struct {
		a, b float64
		want bool
	}{
		{2109.8000000000002, 2109.8, true}, {100, 100.009, true},
		{509.29, 509.30, false}, {100, 100.01, false}, {237.39, 237.29, false},
		{100.01, 100, false}, {100, 100, true},
	} {
		if got := decimalNear(value{Kind: 'n', Number: tc.a}, value{Kind: 'n', Number: tc.b}); got != tc.want {
			t.Fatalf("%v / %v: %v", tc.a, tc.b, got)
		}
	}
}

func TestNormalizedKeysPreserveSourceAndRejectBirthConflicts(t *testing.T) {
	opt := setup(t)
	name, document := "  Алёна  Натиг\u00a0кызы ", " ИД\u00a0 1 "
	fixture(t, opt.Source, SourceFields, [][]any{{"ИП", 45000, name, 2109.8000000000002, "АО", document, "иной", "ОСП", 44900, 44901, 45030, nil, nil, nil}})
	fixture(t, filepath.Join(opt.Exports, "Выгрузка.xlsx"), exportFields, [][]any{{"алена натиг кызы", "ИД 1", 2109.8, "иной", "01.01.1980", ""}})
	r, err := Run(context.Background(), opt, nil)
	if err != nil || r.Found != 1 {
		t.Fatal(r, err)
	}
	rows := readRows(t, filepath.Join(r.Dir, r.Files[3]))
	if rows[1][2] != name || rows[1][5] != document {
		t.Fatal("Изменён исходный текст", rows[1])
	}
	fixture(t, filepath.Join(opt.Exports, "Конфликт.xlsx"), exportFields, [][]any{{"Алёна натиг кызы", "ИД 1", 2109.805, "АО", "02.02.1981", ""}})
	r, err = Run(context.Background(), opt, nil)
	if err != nil || r.Found != 0 {
		t.Fatal("Принят конфликт ДР", r, err)
	}
}
