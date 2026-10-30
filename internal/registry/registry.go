package registry

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/xuri/excelize/v2"
)

//go:embed templates/initiation.xlsx
var initiation []byte

//go:embed templates/termination.xlsx
var termination []byte

var SourceFields = []string{"Регистрационный номер ИП", "Дата возбуждения", "Должник", "Сумма долга", "Взыскатель", "Номер ИД", "Номер дела", "Подразделение ОСП", "Дата вступления решения в законную силу", "Дата выдачи ИД", "Дата завершения ИП", "Подпункт", "Пункт", "Статья"}
var exportFields = []string{"ФИО", "Номер ИД", "Переданная на принудительное исполнение сумма", "Взыскатель", "д.р.", "Номер дела"}
var StageNames = []string{
	"Сверка исходного файла с выгрузкой ОВЗИД",
	"Создание реестра возбуждения по строкам с найденной д.р.",
	"Создание реестра окончания по строкам с найденной д.р.",
	"Создание реестра окончания по строкам без д.р.",
	"Создание реестра аналитики по всем строкам исходного файла",
}

type Options struct {
	Source, Exports string
	Month           time.Month
	Year            int
	SkipAnalytics   bool
	// Deprecated: прежний флаг не влияет на обязательный второй поиск и состав файлов.
	SkipCaseRegisters bool
	// Параметры общего запуска. nil ExportFiles означает прежний выбор всей папки.
	OutputDir   string
	ExportFiles []string
}

func BirthSummary(found, byCase int, _ bool, missing ...int) string {
	empty := 0
	if len(missing) > 0 {
		empty = missing[0]
	}
	return fmt.Sprintf("Совпало: %d (по ИД: %d, по номеру дела: %d). ДР заполнена: %d; пустая ДР: %d", found, found-byCase, byCase, found-empty, empty)
}

type Event struct {
	Stage                 int
	State, Detail         string
	Checked, Total, Found int
	WithoutBirth          int
	FoundByCase           int
	ReadRows              int
}
type Result struct {
	Dir          string
	Total, Found int
	WithoutBirth int
	FoundByCase  int
	ReadRows     int
	Files        []string
}
type record struct {
	MatchedWithoutBirth bool
	Values              []value
	Birthday            time.Time
	Status              string
}
type key [3]value
type birthInfo struct {
	Dates            map[string]time.Time
	Missing, Invalid bool
	Matched          bool
}

func emptyBirthMatch(info *birthInfo, valid bool) bool {
	return valid && info != nil && info.Matched && info.Missing && !info.Invalid && len(info.Dates) == 0
}

func (b *birthInfo) add(v value, date1904 bool) {
	b.Matched = true
	if v.empty() {
		b.Missing = true
	} else if t, valid := birthday(v, date1904); valid {
		b.Dates[t.Format("2006-01-02")] = t
	} else {
		b.Invalid = true
	}
}
func resolveBirth(k key, valid bool, index map[key]*birthInfo) (time.Time, string) {
	if !valid {
		return time.Time{}, "Не заполнены поля ключа"
	}
	info := index[k]
	if info == nil {
		return time.Time{}, "Точное совпадение не найдено"
	}
	switch {
	case len(info.Dates) > 1:
		return time.Time{}, "Полностью совпало; найдены разные д.р."
	case info.Invalid:
		return time.Time{}, "Полностью совпало; некорректная д.р. в выгрузке"
	case len(info.Dates) == 1:
		for _, t := range info.Dates {
			return t, "Полностью совпало"
		}
	case info.Missing:
		return time.Time{}, "Полностью совпало; д.р. отсутствует"
	}
	return time.Time{}, "Точное совпадение не найдено"
}

func makeKey(row []value, cols map[string]int, names []string) (key, bool) {
	var k key
	for i, name := range names {
		k[i] = at(row, cols[name])
		if k[i].empty() {
			return k, false
		}
		if k[i].Kind == 'n' {
			k[i].Text = ""
		}
	}
	if k[0].Kind == 's' {
		k[0].Text = strings.ToLower(k[0].Text)
	}
	return k, true
}
func birthday(v value, date1904 bool) (time.Time, bool) {
	var t time.Time
	if v.Kind == 'n' {
		t, _ = excelize.ExcelDateToTime(v.Number, date1904)
	} else {
		for _, layout := range []string{"02.01.2006", "2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
			if parsed, e := time.Parse(layout, v.Text); e == nil {
				t = parsed
				break
			}
		}
	}
	if t.IsZero() || t.Year() < 1900 || t.After(time.Now()) {
		return time.Time{}, false
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
}
func Names(month time.Month, year int) ([]string, error) {
	if month < 1 || month > 12 || year < 1900 || year > 9999 {
		return nil, fmt.Errorf("выберите корректные месяц и год")
	}
	prefix := []string{"Янв", "Фев", "Мар", "Апр", "Май", "Июн", "Июл", "Авг", "Сен", "Окт", "Ноя", "Дек"}[month-1] + fmt.Sprintf("%02d", year%100) + "_"
	return []string{prefix + "Возб_Изв_Отк_С_ДР.xlsx", prefix + "Окон_Изв_Отказы_С_ДР.xlsx", prefix + "Окон_Изв_Отк_БЕЗ_ДР.xlsx", prefix + "Аналитика.xlsx"}, nil
}
func reserve(parent string, names []string) (string, error) {
	for i := 0; ; i++ {
		name := "Сверка"
		if i > 0 {
			name = fmt.Sprintf("Сверка (%d)", i)
		}
		dir := filepath.Join(parent, name)
		if _, err := os.Stat(dir); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		for _, n := range names {
			full := filepath.Join(dir, n)
			if len(utf16.Encode([]rune(full))) > 218 {
				return "", fmt.Errorf("путь результата длиннее 218 символов: %s\nПеренесите исходный файл в папку с более коротким путём и повторите запуск. Номер подпапки «Сверка» также учитывается", full)
			}
		}
		err := os.Mkdir(dir, 0700)
		if os.IsExist(err) {
			continue
		}
		return dir, err
	}
}

func enabledNames(opt Options) ([]string, error) {
	names, err := Names(opt.Month, opt.Year)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for i, name := range names {
		if !(i == 3 && opt.SkipAnalytics) {
			out = append(out, name)
		}
	}
	return out, nil
}

// PrepareOutput резервирует отдельный комплект до начала общей выгрузки.
func PrepareOutput(opt Options) (string, error) {
	names, err := enabledNames(opt)
	if err != nil {
		return "", err
	}
	source, err := filepath.Abs(opt.Source)
	if err != nil {
		return "", err
	}
	return reserve(filepath.Dir(source), names)
}

// CheckSource проверяет исходник до отправки длительных запросов выгрузки.
func CheckSource(ctx context.Context, path string) error {
	found := false
	err := scanBook(ctx, path, func(_ int, row []value, _ bool) error {
		if found {
			return nil
		}
		found = true
		_, err := headers(row, SourceFields)
		return err
	})
	if err == nil && !found {
		return fmt.Errorf("исходный файл пуст")
	}
	return err
}

// CheckExport не позволяет принять техническую книгу или пустую неподтверждённую выгрузку.
func CheckExport(ctx context.Context, path string, _ bool) error {
	found, rows := false, 0
	err := scanBook(ctx, path, func(_ int, row []value, _ bool) error {
		if !found {
			found = true
			required := []string{"ФИО", "Номер ИД", "Переданная на принудительное исполнение сумма", "д.р."}
			_, err := headers(row, required)
			return err
		}
		for _, v := range row {
			if !v.empty() {
				rows++
				break
			}
		}
		return nil
	})
	if err == nil && (!found || rows == 0) {
		err = fmt.Errorf("выгрузка не содержит строк; пустой отбор не подтверждён Пирамидой")
	}
	return err
}

func Run(ctx context.Context, opt Options, emit func(Event)) (result Result, err error) {
	stage := 0
	send := func(state, detail string, checked, total, found int) {
		if emit != nil {
			emit(Event{Stage: stage, State: state, Detail: detail, Checked: checked, Total: total, Found: found, FoundByCase: result.FoundByCase, ReadRows: result.ReadRows, WithoutBirth: result.WithoutBirth})
		}
	}
	defer func() {
		if err != nil {
			state := "Ошибка"
			if errors.Is(err, context.Canceled) {
				state = "Отменён"
			}
			send(state, err.Error(), 0, result.Total, result.Found)
		}
	}()
	names, err := Names(opt.Month, opt.Year)
	if err != nil {
		return result, err
	}
	sourcePath, err := filepath.Abs(opt.Source)
	if err != nil {
		return result, err
	}
	exportPath, err := filepath.Abs(opt.Exports)
	if err != nil {
		return result, err
	}
	if strings.EqualFold(filepath.Dir(sourcePath), exportPath) {
		return result, fmt.Errorf("исходный файл не должен находиться в выбранной папке выгрузки ОВЗИД")
	}
	enabled := func(kind int) bool { return !(kind == 3 && opt.SkipAnalytics) }
	outputNames := []string{}
	for i, name := range names {
		if enabled(i) {
			outputNames = append(outputNames, name)
		}
	}
	dir := opt.OutputDir
	owned := dir == ""
	if owned {
		dir, err = reserve(filepath.Dir(sourcePath), outputNames)
	} else {
		dir, err = filepath.Abs(dir)
		if err == nil {
			var info os.FileInfo
			info, err = os.Stat(dir)
			if err == nil && !info.IsDir() {
				err = fmt.Errorf("папка результата занята файлом")
			}
		}
		for _, name := range outputNames {
			if err != nil {
				break
			}
			path := filepath.Join(dir, name)
			if len(utf16.Encode([]rune(path))) > 218 {
				err = fmt.Errorf("путь результата длиннее 218 символов: %s", path)
				break
			}
			if _, e := os.Lstat(path); e == nil {
				err = fmt.Errorf("файл результата уже существует: %s", name)
			} else if !os.IsNotExist(e) {
				err = e
			}
		}
	}
	if err != nil {
		return result, err
	}
	success := false
	published := []string{}
	defer func() {
		if !success {
			var cleanup error
			if owned {
				cleanup = os.RemoveAll(dir)
			} else {
				for _, path := range published {
					cleanup = errors.Join(cleanup, os.Remove(path))
				}
			}
			if cleanup != nil {
				err = errors.Join(err, fmt.Errorf("не удалось удалить незавершённую папку %s: %w", dir, cleanup))
			}
		}
	}()
	send("Выполняется", "Чтение исходного файла…", 0, 0, 0)
	var sourceHeaders []value
	var sourceCols map[string]int
	rows := []record{}
	wanted := map[key]*birthInfo{}
	wantedByCase := map[key]*birthInfo{}
	sourceKeys := []string{"Должник", "Номер ИД", "Сумма долга"}
	sourceCaseKeys := []string{"Должник", "Номер дела", "Сумма долга"}
	exportCaseKeys := exportFields[:3]
	err = scanBook(ctx, sourcePath, func(_ int, row []value, date1904 bool) error {
		if sourceCols == nil {
			var e error
			sourceCols, e = headers(row, SourceFields)
			sourceHeaders = append([]value(nil), row...)
			return e
		}
		empty := true
		for _, v := range row {
			if !v.empty() {
				empty = false
				break
			}
		}
		if empty {
			return nil
		}
		if len(row) > len(sourceHeaders) {
			return fmt.Errorf("в исходнике есть данные за пределами заголовков")
		}
		for len(row) < len(sourceHeaders) {
			row = append(row, value{})
		}
		if date1904 {
			for _, field := range []string{"Дата возбуждения", "Дата вступления решения в законную силу", "Дата выдачи ИД", "Дата завершения ИП"} {
				i := sourceCols[field]
				if row[i].Kind == 'n' {
					row[i].Number += 1462
				}
			}
		}
		rows = append(rows, record{Values: row})
		if k, ok := makeKey(row, sourceCols, sourceKeys); ok {
			if wanted[k] == nil {
				wanted[k] = &birthInfo{Dates: map[string]time.Time{}}
			}
		}
		if k, ok := makeKey(row, sourceCols, sourceCaseKeys); ok && wantedByCase[k] == nil {
			wantedByCase[k] = &birthInfo{Dates: map[string]time.Time{}}
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("исходный файл: %w", err)
	}
	if sourceCols == nil {
		return result, fmt.Errorf("исходный файл пуст")
	}
	result.Total = len(rows)
	exports := []string{}
	if opt.ExportFiles == nil {
		entries, e := os.ReadDir(exportPath)
		if e != nil {
			return result, e
		}
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".xlsx") && !strings.HasPrefix(e.Name(), "~$") {
				exports = append(exports, filepath.Join(exportPath, e.Name()))
			}
		}
	} else {
		for _, file := range opt.ExportFiles {
			full, e := filepath.Abs(file)
			if e != nil || !strings.EqualFold(filepath.Dir(full), exportPath) || !strings.EqualFold(filepath.Ext(full), ".xlsx") {
				return result, fmt.Errorf("некорректный путь подтверждённой выгрузки: %s", file)
			}
			exports = append(exports, full)
		}
	}
	if len(exports) == 0 && opt.ExportFiles == nil {
		return result, fmt.Errorf("в папке нет XLSX-файлов выгрузки ОВЗИД")
	}
	for i, file := range exports {
		reportRead := func() {
			send("Выполняется", fmt.Sprintf("Прочитано строк ОВЗИД: %d · файл %d/%d · %s", result.ReadRows, i+1, len(exports), filepath.Base(file)), 0, len(rows), 0)
		}
		reportRead()
		before, e := os.Stat(file)
		if e != nil {
			return result, e
		}
		var cols map[string]int
		e = scanBook(ctx, file, func(_ int, row []value, date1904 bool) error {
			if cols == nil {
				var e error
				required := []string{"ФИО", "Номер ИД", "Переданная на принудительное исполнение сумма", "д.р."}
				cols, e = headers(row, required)
				return e
			}
			empty := true
			for _, v := range row {
				if !v.empty() {
					empty = false
					break
				}
			}
			if empty {
				return nil
			}
			result.ReadRows++
			if result.ReadRows%2000 == 0 {
				reportRead()
			}
			v := at(row, cols["д.р."])
			if k, ok := makeKey(row, cols, exportFields[:3]); ok && wanted[k] != nil {
				wanted[k].add(v, date1904)
			}
			if k, ok := makeKey(row, cols, exportCaseKeys); ok && wantedByCase[k] != nil {
				wantedByCase[k].add(v, date1904)
			}
			return nil
		})
		if e != nil {
			return result, fmt.Errorf("выгрузка «%s»: %w", filepath.Base(file), e)
		}
		if cols == nil {
			return result, fmt.Errorf("выгрузка «%s» пуста", filepath.Base(file))
		}
		after, e := os.Stat(file)
		if e != nil {
			return result, e
		}
		if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return result, fmt.Errorf("выгрузка «%s» изменилась во время чтения; повторите запуск после завершения выгрузки", filepath.Base(file))
		}
		reportRead()
	}
	for i := range rows {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		r := &rows[i]
		k, ok := makeKey(r.Values, sourceCols, sourceKeys)
		r.Birthday, r.Status = resolveBirth(k, ok, wanted)
		r.MatchedWithoutBirth = emptyBirthMatch(wanted[k], ok)
		// Проверяем отсутствие самого совпадения ключа, а не только отсутствие найденной ДР.
		if info := wanted[k]; !ok || info == nil || !info.Matched {
			ck, cok := makeKey(r.Values, sourceCols, sourceCaseKeys)
			r.Birthday, r.Status = resolveBirth(ck, cok, wantedByCase)
			r.MatchedWithoutBirth = emptyBirthMatch(wantedByCase[ck], cok)
			if !r.Birthday.IsZero() || r.MatchedWithoutBirth {
				result.FoundByCase++
				r.Status = "Совпало по номеру дела исходника и номеру ИД выгрузки"
				if r.MatchedWithoutBirth {
					r.Status += "; д.р. отсутствует"
				}
			} else {
				r.Status = "По номеру дела: " + r.Status
			}
		}
		if !r.Birthday.IsZero() || r.MatchedWithoutBirth {
			result.Found++
			if r.MatchedWithoutBirth {
				result.WithoutBirth++
			}
		}
		if i%100 == 0 || i+1 == len(rows) {
			send("Выполняется", fmt.Sprintf("Проверено %d/%d. %s", i+1, len(rows), BirthSummary(result.Found, result.FoundByCase, opt.SkipCaseRegisters, result.WithoutBirth)), i+1, len(rows), result.Found)
		}
	}
	send("Готово", fmt.Sprintf("Проверено %d/%d. %s", len(rows), len(rows), BirthSummary(result.Found, result.FoundByCase, opt.SkipCaseRegisters, result.WithoutBirth)), len(rows), len(rows), result.Found)
	// Полный комплект готовится вне папки результата: в ней не бывает частично записанных XLSX.
	tmp, err := os.MkdirTemp("", "fssp-registers-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmp)
	for i := range names {
		stage = i + 1
		if !enabled(i) {
			send("Пропущен", "Отключено в настройках", 0, len(rows), result.Found)
			continue
		}
		send("Выполняется", "Формирование файла…", 0, len(rows), result.Found)
		if err = writeBook(ctx, filepath.Join(tmp, names[i]), i, sourceHeaders, sourceCols, rows); err != nil {
			return result, err
		}
		detail := "Файл сформирован; ожидает сохранения комплекта"
		send("Подготовлен", detail, 0, len(rows), result.Found)
	}
	for _, name := range outputNames {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if err = copyFile(ctx, filepath.Join(tmp, name), filepath.Join(dir, name)); err != nil {
			return result, err
		}
		published = append(published, filepath.Join(dir, name))
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	result.Dir = dir
	result.Files = outputNames
	success = true
	for i := 1; i <= len(names); i++ {
		if !enabled(i - 1) {
			continue
		}
		stage = i
		send("Сохранён", names[i-1], len(rows), len(rows), result.Found)
	}
	return result, nil
}

func writeBook(ctx context.Context, dest string, kind int, sourceHeaders []value, cols map[string]int, rows []record) error {
	var f *excelize.File
	var err error
	if kind == 0 {
		f, err = excelize.OpenReader(bytes.NewReader(initiation))
	} else if kind < 3 {
		f, err = excelize.OpenReader(bytes.NewReader(termination))
	} else {
		f = excelize.NewFile()
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sheet := f.GetSheetList()[0]
	outHeaders := []string{}
	if kind != 3 {
		h, err := f.GetRows(sheet)
		if err != nil {
			return err
		}
		outHeaders = h[0]
	} else {
		for _, v := range sourceHeaders {
			outHeaders = append(outHeaders, v.Text)
		}
		outHeaders = append(outHeaders, "д.р.", "Результат сопоставления")
		for i, h := range outHeaders {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			if err = f.SetCellStr(sheet, cell, h); err != nil {
				return err
			}
		}
		if err = f.SetColWidth(sheet, "A", "P", 23); err != nil {
			return err
		}
		if err = f.SetColWidth(sheet, "C", "C", 38); err != nil {
			return err
		}
		if err = f.SetColWidth(sheet, "E", "E", 58); err != nil {
			return err
		}
		last, _ := excelize.ColumnNumberToName(len(outHeaders))
		if err = f.SetColWidth(sheet, last, last, 64); err != nil {
			return err
		}
		style, e := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"253D57"}, Pattern: 1}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "center"}})
		if e != nil {
			return e
		}
		if err = f.SetCellStyle(sheet, "A1", last+"1", style); err != nil {
			return err
		}
		if err = f.SetRowHeight(sheet, 1, 48); err != nil {
			return err
		}
		if err = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
			return err
		}
	}
	styles := make([]int, len(outHeaders))
	dateFormat := "dd.mm.yyyy"
	for i, h := range outHeaders {
		if kind != 3 {
			cell, _ := excelize.CoordinatesToCellName(i+1, 2)
			styles[i], err = f.GetCellStyle(sheet, cell)
		} else if strings.HasPrefix(h, "Дата ") || h == "д.р." {
			styles[i], err = f.NewStyle(&excelize.Style{CustomNumFmt: &dateFormat})
		} else if h == "Сумма долга" {
			styles[i], err = f.NewStyle(&excelize.Style{NumFmt: 4})
		}
		if err != nil {
			return err
		}
	}
	rowNum := 2
	for _, r := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		birth := r.Birthday
		matched := !birth.IsZero() || r.MatchedWithoutBirth
		if kind < 2 && !matched || kind == 2 && matched {
			continue
		}
		for c, h := range outHeaders {
			var v any
			if h == "Д.р. должника" || kind == 3 && c == len(outHeaders)-2 {
				if !birth.IsZero() {
					v = birth
				}
			} else if kind == 3 && c == len(outHeaders)-1 {
				v = r.Status
			} else if kind == 3 {
				v = r.Values[c].any()
			} else if i, ok := cols[h]; ok {
				v = at(r.Values, i).any()
			} else {
				return fmt.Errorf("столбец шаблона «%s» отсутствует в исходнике", h)
			}
			cell, _ := excelize.CoordinatesToCellName(c+1, rowNum)
			if text, ok := v.(string); ok {
				err = f.SetCellStr(sheet, cell, text)
			} else {
				err = f.SetCellValue(sheet, cell, v)
			}
			if err != nil {
				return err
			}
			if err = f.SetCellStyle(sheet, cell, cell, styles[c]); err != nil {
				return err
			}
		}
		rowNum++
	}
	if kind == 3 {
		last, _ := excelize.CoordinatesToCellName(len(outHeaders), max(1, rowNum-1))
		if err = f.AutoFilter(sheet, "A1:"+last, []excelize.AutoFilterOptions{}); err != nil {
			return err
		}
	}
	if err = f.SaveAs(dest); err != nil {
		return err
	}
	return validateBook(dest, len(outHeaders), rowNum-2)
}

func validateBook(filename string, columns, count int) error {
	f, err := excelize.OpenFile(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetList()[0])
	if err != nil {
		return err
	}
	if len(rows) == 0 || len(rows[0]) != columns {
		return fmt.Errorf("неверный заголовок сформированного файла")
	}
	n := 0
	for _, r := range rows[1:] {
		nonempty := false
		for _, v := range r {
			if v != "" {
				nonempty = true
				break
			}
		}
		if nonempty {
			n++
		}
	}
	if n != count {
		return fmt.Errorf("неверное число строк сформированного файла: %d вместо %d", n, count)
	}
	return nil
}
