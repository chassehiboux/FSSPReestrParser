package registry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func fixture(t *testing.T, path string, head []string, rows [][]any) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	s := f.GetSheetList()[0]
	for i, h := range head {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if e := f.SetCellStr(s, cell, h); e != nil {
			t.Fatal(e)
		}
	}
	for r, row := range rows {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if e := f.SetCellValue(s, cell, v); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := f.SaveAs(path); e != nil {
		t.Fatal(e)
	}
}
func setup(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	exports := filepath.Join(root, "ОВЗИД")
	if e := os.Mkdir(exports, 0700); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(root, "Извещения.xlsx")
	row := func(name, id string, amount any, party string) []any {
		return []any{"1/26-ИП", 45000, name, amount, party, id, "дело", "ОСП", 44900, 44901, 45030, "4", "1", "31"}
	}
	fixture(t, source, SourceFields, [][]any{
		row("Иванов Иван Иванович", "A", 100.25, "АО"), row("Иванов Иван Иванович", "A", 100.25, "АО"),
		row("Петров Пётр Петрович", "B", 200, "АО"), row("Сидоров Иван", "C", 300, "АО"), row("Двойной Иван", "D", 400, "АО"),
		row(" Иванов Иван Иванович", "A", 100.25, "АО"), row("Иванов Иван Иванович", "A", "100.25", "АО"), row("Иванов Иван Иванович", "A", 100.25, "ао"),
	})
	fixture(t, filepath.Join(exports, "Выгрузка.xlsx"), exportFields, [][]any{
		{"ИВАНОВ ИВАН ИВАНОВИЧ", "A", 100.25, "АО", "12.12.1980", "дело"}, {"Иванов Иван Иванович", "A", 100.25, "АО", "12.12.1980", "дело"},
		{"Петров Пётр Петрович", "B", 200, "АО", nil, "дело"}, {"Сидоров Иван", "C", 300, "АО", "ошибка", "дело"},
		{"Двойной Иван", "D", 400, "АО", "01.01.1980", "дело"}, {"Двойной Иван", "D", 400, "АО", "02.01.1980", "дело"},
	})
	return Options{Source: source, Exports: exports, Month: time.September, Year: 2026}
}

func TestOptionalOutputs(t *testing.T) {
	for _, analytics := range []bool{false, true} {
		for _, cases := range []bool{false, true} {
			opt := setup(t)
			opt.SkipAnalytics, opt.SkipCaseRegisters = analytics, cases
			if cases {
				fixture(t, filepath.Join(opt.Exports, "Выгрузка.xlsx"), exportFields[:5], [][]any{{"Иванов Иван Иванович", "A", 100.25, "АО", "12.12.1980"}})
			}
			var skipped int
			r, err := Run(context.Background(), opt, func(ev Event) {
				if ev.State == "Пропущен" {
					skipped++
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			want := 4
			if analytics {
				want--
			}
			entries, err := os.ReadDir(r.Dir)
			if err != nil || len(entries) != want || len(r.Files) != want || skipped != 4-want || r.Found != 3 {
				t.Fatalf("аналитика=%v дела=%v: %+v файлы=%d пропуски=%d ошибка=%v", analytics, cases, r, len(entries), skipped, err)
			}
			for _, file := range r.Files {
				if analytics && strings.Contains(file, "Аналитика") || cases && strings.Contains(file, "по_делу") {
					t.Fatal("создан отключённый файл", file)
				}
			}
		}
	}
}

func TestExplicitOutputPreservesInputsOnFailure(t *testing.T) {
	opt := setup(t)
	dir, err := PrepareOutput(opt)
	if err != nil {
		t.Fatal(err)
	}
	inputs := filepath.Join(dir, "Исх.")
	if err = os.Mkdir(inputs, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(inputs, "сохранённая выгрузка.xlsx")
	if err = os.WriteFile(marker, []byte("не трогать"), 0600); err != nil {
		t.Fatal(err)
	}
	opt.OutputDir = dir
	opt.ExportFiles = []string{} // Подтверждено, что все отборы пустые.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = Run(ctx, opt, func(ev Event) {
		if ev.Stage == 1 && ev.State == "Подготовлен" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "не трогать" {
		t.Fatal("потеряны выгрузки при ошибке реестров", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("остался неполный реестр", err)
	}
}
func readRows(t *testing.T, path string) [][]string {
	t.Helper()
	f, e := excelize.OpenFile(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	r, e := f.GetRows(f.GetSheetList()[0])
	if e != nil {
		t.Fatal(e)
	}
	for len(r) > 1 {
		empty := true
		for _, v := range r[len(r)-1] {
			if v != "" {
				empty = false
			}
		}
		if !empty {
			break
		}
		r = r[:len(r)-1]
	}
	return r
}
func TestStrictOutputsAndRepeatedRun(t *testing.T) {
	opt := setup(t)
	var events []Event
	r, e := Run(context.Background(), opt, func(e Event) { events = append(events, e) })
	if e != nil {
		t.Fatal(e)
	}
	if r.Found != 3 || r.Total != 8 || r.FoundByCase != 0 {
		t.Fatalf("%+v", r)
	}
	counts := []int{3, 3, 5, 8}
	if len(r.Files) != len(counts) {
		t.Fatal("нет пятого файла", r.Files)
	}
	for i, name := range r.Files {
		rows := readRows(t, filepath.Join(r.Dir, name))
		if len(rows)-1 != counts[i] {
			t.Fatalf("%s: %d", name, len(rows)-1)
		}
		if i == 1 || i == 2 {
			if !reflect.DeepEqual(rows[0], SourceFields) {
				t.Fatal("изменён шаблон окончания", rows[0])
			}
		}
	}
	f, e := excelize.OpenFile(filepath.Join(r.Dir, r.Files[0]))
	if e != nil {
		t.Fatal(e)
	}
	if v, _ := f.GetCellValue(f.GetSheetList()[0], "F2"); !strings.Contains(v, "1980") {
		t.Fatal("не записана ДР", v)
	}
	f.Close()
	for i := 1; i <= len(r.Files); i++ {
		found := false
		for _, ev := range events {
			if ev.Stage == i && ev.State == "Сохранён" {
				found = true
			}
		}
		if !found {
			t.Fatal("нет завершённого этапа", i)
		}
	}
	before, e := os.ReadFile(filepath.Join(r.Dir, r.Files[0]))
	if e != nil {
		t.Fatal(e)
	}
	r2, e := Run(context.Background(), opt, nil)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Base(r2.Dir) != "Сверка (1)" {
		t.Fatal(r2.Dir)
	}
	after, e := os.ReadFile(filepath.Join(r.Dir, r.Files[0]))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("изменён прежний комплект")
	}
	analytics := readRows(t, filepath.Join(r.Dir, r.Files[3]))
	if !strings.Contains(analytics[3][15], "отсутствует") || !strings.Contains(analytics[4][15], "некорректная") || !strings.Contains(analytics[5][15], "разные") {
		t.Fatal("потеряны причины отсутствия ДР")
	}
}
func TestCancellationAndBrokenExportLeaveNoPartialSet(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(strings.TrimSpace(map[bool]string{false: "отмена", true: "повреждение"}[broken]), func(t *testing.T) {
			opt := setup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if broken {
				if e := os.WriteFile(filepath.Join(opt.Exports, "Ошибка.xlsx"), []byte("HTML"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			_, e := Run(ctx, opt, func(e Event) {
				if !broken && e.Stage == 1 && e.State == "Подготовлен" {
					cancel()
				}
			})
			if e == nil {
				t.Fatal("ожидалась ошибка")
			}
			if !broken && !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			if _, e = os.Stat(filepath.Join(filepath.Dir(opt.Source), "Сверка")); !os.IsNotExist(e) {
				t.Fatal("остался незавершённый комплект", e)
			}
		})
	}
}
func TestEmptySourceAndLongPath(t *testing.T) {
	opt := setup(t)
	fixture(t, opt.Source, SourceFields, nil)
	r, e := Run(context.Background(), opt, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.Total != 0 || r.Found != 0 {
		t.Fatal(r)
	}
	for _, name := range r.Files {
		if len(readRows(t, filepath.Join(r.Dir, name))) != 1 {
			t.Fatal("пустая категория не содержит только заголовок")
		}
	}
	names, _ := Names(time.September, 2026)
	if _, e = reserve(filepath.Join(t.TempDir(), strings.Repeat("я", 180)), names); e == nil || !strings.Contains(e.Error(), "218") {
		t.Fatal("не проверена длина пути", e)
	}
}
func TestRealWorkbook(t *testing.T) {
	source, exports := os.Getenv("FSSP_REGISTRY_SOURCE"), os.Getenv("FSSP_REGISTRY_EXPORTS")
	if source == "" || exports == "" {
		t.Skip("реальные файлы передаются переменными среды")
	}
	r, e := Run(context.Background(), Options{Source: source, Exports: exports, Month: time.September, Year: 2026}, func(e Event) {
		if e.State != "Выполняется" || e.Stage == 0 && e.Checked == 0 {
			t.Log(e.Stage, e.State, e.Detail)
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	if r.Total != 3376 {
		t.Fatalf("неожиданный результат %+v", r)
	}
	counts := []int{r.Found, r.Found, r.Total - r.Found, r.Total}
	if len(r.Files) != 4 {
		t.Fatal("нет шести файлов")
	}
	for i, n := range r.Files {
		if len(readRows(t, filepath.Join(r.Dir, n)))-1 != counts[i] {
			t.Fatal(n)
		}
	}
	t.Log("Результаты:", r.Dir, "Найдено по номеру дела:", r.FoundByCase)
}

func TestCaseNumberMatchesUnmatchedID(t *testing.T) {
	opt := setup(t)
	row := func(fio, id, caseNumber string, amount any, party string) []any {
		return []any{"ИП", 45000, fio, amount, party, id, caseNumber, "ОСП", 44900, 44901, 45030, nil, nil, nil}
	}
	fixture(t, opt.Source, SourceFields, [][]any{
		row("Иванов Иван", "другой ИД", "К-1", 100, "АО"),
		row("Петров Пётр", "ИД-2", "другое дело", 200, "АО"),
		row("Двойной Иван", "нет ИД", "К-3", 300, "АО"),
		row("Иванов Иван", "другой ИД", "К-1 ", 100, "АО"),
		row("Иванов Иван", "другой ИД", "", 100, "АО"),
		row("Иванов Иван", "другой ИД", "К-1", 100, "АО"),
		row("Иванов Иван", "другой ИД", "К-1", "100", "АО"),
		row("Иванов Иван", "другой ИД", "К-1", 100, "ао"),
	})
	fixture(t, filepath.Join(opt.Exports, "Выгрузка.xlsx"), exportFields, [][]any{
		{"ИВАНОВ ИВАН", "К-1", 100, "АО", "01.01.1980", "К-1"},
		{"Иванов Иван", "К-1", 100, "АО", "01.01.1980", "К-1"},
		{"Петров Пётр", "ИД-2", 200, "АО", "02.01.1980", "К-2"},
		{"Двойной Иван", "К-3", 300, "АО", "03.01.1980", "К-3"},
		{"Двойной Иван", "К-3", 300, "АО", "04.01.1980", "К-3"},
	})
	r, e := Run(context.Background(), opt, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.Found != 4 || r.FoundByCase != 3 {
		t.Fatal("Не объединены способы поиска", r)
	}
	names, _ := Names(opt.Month, opt.Year)
	if len(r.Files) != 4 {
		t.Fatal("Созданы лишние файлы", r.Files)
	}
	main := readRows(t, filepath.Join(r.Dir, names[0]))
	ending := readRows(t, filepath.Join(r.Dir, names[1]))
	missing := readRows(t, filepath.Join(r.Dir, names[2]))
	if len(main) != 5 || len(ending) != 5 || len(missing) != 5 {
		t.Fatal("Неверный состав объединённых реестров")
	}
	if main[1][0] != "Иванов Иван" || main[2][0] != "Петров Пётр" || main[1][8] != "другой ИД" {
		t.Fatal("Нарушен порядок или заменён исходный номер ИД", main)
	}
	if !reflect.DeepEqual(ending[0], SourceFields) {
		t.Fatal("Изменён шаблон окончания")
	}
	if !strings.Contains(main[1][5], "1980") {
		t.Fatal("Не записана ДР второго поиска")
	}
	analytics := readRows(t, filepath.Join(r.Dir, names[3]))
	if !strings.Contains(analytics[1][15], "номеру дела") {
		t.Fatal("Потерян способ сопоставления", analytics[1])
	}
}

func TestOriginalMatchExcludesCaseEvenWithoutUsableBirth(t *testing.T) {
	opt := setup(t)
	rows := [][]any{}
	exports := [][]any{}
	for i, name := range []string{"Полное совпадение", "Нет даты", "Конфликт дат", "Некорректная дата"} {
		id, caseNumber := name+" ИД", name+" дело"
		amount := 100 + i
		rows = append(rows, []any{"ИП", 45000, name, amount, "АО", id, caseNumber, "ОСП", 44900, 44901, 45030, nil, nil, nil})
		var born any = "01.01.1980"
		if i == 1 {
			born = nil
		}
		if i == 3 {
			born = "ошибка"
		}
		exports = append(exports, []any{name, id, amount, "АО", born, "другое дело"})
		if i == 2 {
			exports = append(exports, []any{name, id, amount, "АО", "02.01.1980", "ещё дело"})
		}
		// По делу дата единственная и корректная, но исходный ключ уже совпал.
		exports = append(exports, []any{name, caseNumber, amount, "АО", "01.01.1980", caseNumber})
	}
	fixture(t, opt.Source, SourceFields, rows)
	fixture(t, filepath.Join(opt.Exports, "Выгрузка.xlsx"), exportFields, exports)
	r, e := Run(context.Background(), opt, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.Found != 1 || r.FoundByCase != 0 {
		t.Fatal("совпавшие исходные ключи ошибочно включены по делу", r)
	}
	if len(r.Files) != 4 {
		t.Fatal("Созданы отдельные файлы по делу", r.Files)
	}
}

func TestExportReadCounterContinuesAcrossFiles(t *testing.T) {
	opt := setup(t)
	fixture(t, filepath.Join(opt.Exports, "Я-второй.xlsx"), exportFields, [][]any{
		{nil, nil, nil, nil, nil, nil},
		{"Другой должник", "ИД", 10, "АО", nil, "дело"},
		{"Ещё должник", "ИД2", 20, "АО", nil, "дело2"},
	})
	var events []Event
	r, err := Run(context.Background(), opt, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if r.ReadRows != 8 {
		t.Fatal("неверный общий счётчик, заголовки и пустые строки не учитываются", r.ReadRows)
	}
	last := 0
	sawBoundary := false
	sawFinal := false
	for _, e := range events {
		if e.ReadRows < last {
			t.Fatalf("счётчик сбросился: %d → %d", last, e.ReadRows)
		}
		last = e.ReadRows
		if strings.Contains(e.Detail, "Я-второй.xlsx") && e.ReadRows == 6 {
			sawBoundary = true
		}
		if strings.Contains(e.Detail, "Прочитано строк ОВЗИД: 8") {
			sawFinal = true
		}
	}
	if !sawBoundary || !sawFinal {
		t.Fatal("нет накопительного счётчика при переходе и завершении файла")
	}
}
