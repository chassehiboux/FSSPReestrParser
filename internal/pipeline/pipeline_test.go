package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/chassehiboux/FSSPReestrParser/internal/registry"
	"github.com/xuri/excelize/v2"
)

func book(path string, rows [][]any) error {
	f := excelize.NewFile()
	defer f.Close()
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &row); err != nil {
			return err
		}
	}
	return f.SaveAs(path)
}
func request(t *testing.T) (Request, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "Исходный.xlsx")
	head := []any{}
	for _, name := range registry.SourceFields {
		head = append(head, name)
	}
	if err := book(source, [][]any{head, {"ИП", 45000, "Иванов Иван", 100, "АО", "ИД-1", "Д-1", "ОСП", 44900, 44901, 45030, "1", "2", "3"}}); err != nil {
		t.Fatal(err)
	}
	p := model.Profile{ID: "test", Name: "Регион", Bases: []model.Base{{ID: "base", URL: "https://example.test"}}, Departments: []model.Department{{BaseID: "base", ID: 1, Name: "Отдел"}}}
	return Request{Registry: registry.Options{Source: source, Month: time.September, Year: 2026, SkipAnalytics: true, SkipCaseRegisters: true}, Profile: p, Parallel: 1}, filepath.Join(root, "local")
}

type fake struct {
	mu       sync.Mutex
	calls    map[string]int
	failID   string
	failures int
	empty    bool
	output   string
	badID    string
}
type instance struct {
	*fake
	dir string
}

func (i instance) Close()                                              {}
func (f *fake) factory(_ context.Context, dir string) (Backend, error) { return instance{f, dir}, nil }
func (i instance) Begin(_ context.Context, j model.Job) (*queue.Pending, error) {
	i.mu.Lock()
	if i.calls == nil {
		i.calls = map[string]int{}
	}
	i.calls[j.ID]++
	count, empty := i.calls[j.ID], i.empty
	failed := i.failID == j.ID && count <= i.failures
	i.mu.Unlock()
	if failed {
		return nil, errors.New("временный сбой запроса")
	}
	state := queue.Empty
	if !empty {
		rows := [][]any{{"ФИО", "Номер ИД", "Переданная на принудительное исполнение сумма", "Взыскатель", "д.р."}, {"ИВАНОВ ИВАН", "ИД-1", 100, "АО", "01.01.1980"}}
		if j.ID == i.badID {
			rows = [][]any{{"Ошибка сервера"}, {"Файл не сформирован"}}
		}
		if err := book(filepath.Join(i.dir, j.Filename), rows); err != nil {
			return nil, err
		}
		state = queue.Saved
	}
	ch := make(chan queue.Event, 1)
	ch <- queue.Event{State: state}
	return &queue.Pending{Events: ch, Close: func() {}}, nil
}
func checkOutputs(t *testing.T, out Result, found int) {
	t.Helper()
	if len(out.Files) != 3 || out.Found != found || out.Total != 1 {
		t.Fatalf("неверный результат: %+v", out)
	}
	for _, name := range out.Files {
		if err := queue.ValidateXLSX(filepath.Join(out.Dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetriesOnlyFailedJobs(t *testing.T) {
	req, local := request(t)
	jobs, _ := model.BuildJobs(req.Profile)
	f := &fake{failID: jobs[1].ID, failures: 2}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 1)
	for _, j := range jobs {
		want := 1
		if j.ID == f.failID {
			want = 3
		}
		if f.calls[j.ID] != want {
			t.Fatal("повторена успешная выгрузка", f.calls)
		}
	}
}

func TestPublishesImmediatelyAndDeletesLocalCopy(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	var plan *Plan
	immediate := false
	_, err := r.Run(context.Background(), req, func(ev Event) {
		if ev.Plan != nil {
			plan = ev.Plan
		}
		if ev.Export != nil && ev.Export.State == queue.Saved {
			name := jobFilename(plan.Jobs, ev.Export.JobID)
			if queue.ValidateXLSX(filepath.Join(plan.Dir, "Исх.", name)) != nil {
				t.Error("нет целевого файла при успешном статусе")
			}
			paths, _ := filepath.Glob(filepath.Join(local, "pipelines", "*", "exports", name))
			if len(paths) > 0 {
				t.Error("локальная копия не удалена")
			}
			count := 0
			for _, n := range f.calls {
				count += n
			}
			if count == 1 {
				immediate = true
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !immediate {
		t.Fatal("первый файл не опубликован до окончания остальных выгрузок")
	}
}

func TestNetworkWriteRetryKeepsDownloadedFilesAndResumes(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	blocked := true
	saves := 0
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond, Save: func(src, dest string) error {
		saves++
		if blocked {
			return errors.New("SMB временно недоступен")
		}
		return queue.SaveResult(src, dest, true)
	}}
	out, err := r.Run(context.Background(), req, nil)
	if err == nil || saves != 3 {
		t.Fatalf("%v, попыток записи=%d", err, saves)
	}
	if len(out.Files) != 0 {
		t.Fatal("начаты реестры до полной записи")
	}
	blocked = false
	resumed := false
	out2, err := r.Run(context.Background(), req, func(ev Event) {
		if ev.Plan != nil {
			resumed = ev.Plan.Resumed
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out2, 1)
	if !resumed || out.Dir != out2.Dir {
		t.Fatal("не продолжен прежний комплект")
	}
	for _, count := range f.calls {
		if count != 1 {
			t.Fatal("из-за сбоя сети повторена выгрузка", f.calls)
		}
	}
}

func TestAutomaticWriteRetryWithoutNewExport(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	calls := 0
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond, Save: func(src, dest string) error {
		calls++
		if calls < 3 {
			return errors.New("занят файл")
		}
		return queue.SaveResult(src, dest, true)
	}}
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 1)
	if calls != len(f.calls)+2 {
		t.Fatal("неверный повтор записи", calls)
	}
	for _, count := range f.calls {
		if count != 1 {
			t.Fatal(f.calls)
		}
	}
}

func TestCancellationResumesWithoutRepeatingSavedJobs(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	ctx, cancel := context.WithCancel(context.Background())
	out, err := r.Run(ctx, req, func(ev Event) {
		if ev.Export != nil && ev.Export.State == "Подготовлен" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	out2, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out2, 1)
	if out.Dir != out2.Dir {
		t.Fatal("после отмены создана новая папка")
	}
	for _, count := range f.calls {
		if count != 1 {
			t.Fatal("повторено готовое задание", f.calls)
		}
	}
}

func TestIncompleteExportNeverStartsRegistry(t *testing.T) {
	req, local := request(t)
	jobs, _ := model.BuildJobs(req.Profile)
	f := &fake{failID: jobs[0].ID, failures: 10}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	registryStarted := false
	out, err := r.Run(context.Background(), req, func(ev Event) {
		if ev.Stage != nil && ev.Stage.Stage > 0 {
			registryStarted = true
		}
	})
	if err == nil || registryStarted || len(out.Files) != 0 {
		t.Fatal("сверка на неполном комплекте", err)
	}
	f.failures = 0
	if _, err = r.Run(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.ID != f.failID && f.calls[j.ID] != 1 {
			t.Fatal("повторено готовое задание")
		}
	}
}

func TestAllEmptyAndNewRunDoesNotOverwrite(t *testing.T) {
	req, local := request(t)
	f := &fake{empty: true}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 0)
	entries, err := os.ReadDir(filepath.Join(out.Dir, "Исх."))
	if err != nil || len(entries) != 0 {
		t.Fatal("пустые отборы превратились в файлы", err)
	}
	out2, err := r.Run(context.Background(), req, nil)
	if err != nil || out2.Dir == out.Dir {
		t.Fatal("перезаписан готовый комплект", err)
	}
	checkOutputs(t, out, 0)
}

func TestCorruptTargetBlocksRegistry(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond, Save: func(_, dest string) error { return os.WriteFile(dest, []byte("HTML вместо книги"), 0600) }}
	registryStarted := false
	_, err := r.Run(context.Background(), req, func(ev Event) {
		if ev.Stage != nil && ev.Stage.Stage > 0 {
			registryStarted = true
		}
	})
	if err == nil || registryStarted {
		t.Fatal("повреждённая запись пропущена в сверку")
	}
}

func TestSessionLockAndChangedSource(t *testing.T) {
	req, local := request(t)
	r := Runner{Local: local}
	s, _, err := r.open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.open(context.Background(), req); err == nil {
		s.close()
		t.Fatal("один запуск открыт дважды")
	}
	s.close()
	if err = book(req.Registry.Source, [][]any{{"неверные поля"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.open(context.Background(), req); err == nil {
		t.Fatal("изменившийся исходник принят в прежний запуск")
	}
}

func TestRecoversFileSavedBeforeCheckpoint(t *testing.T) {
	req, local := request(t)
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	s, _, err := r.open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	j := s.Jobs[0]
	backend, _ := f.factory(context.Background(), filepath.Join(s.work, "exports"))
	if _, err = backend.Begin(context.Background(), j); err != nil {
		s.close()
		t.Fatal(err)
	}
	s.close() // Журнал ещё не содержит Saved, но атомарно сохранённая книга уже существует.
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 1)
	if f.calls[j.ID] != 1 {
		t.Fatal("повторён файл, переживший аварию до записи журнала")
	}
}

func TestWrongExportHeadersAreRetriedAndNotReused(t *testing.T) {
	req, local := request(t)
	jobs, _ := model.BuildJobs(req.Profile)
	f := &fake{badID: jobs[0].ID}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	_, err := r.Run(context.Background(), req, nil)
	if err == nil || f.calls[f.badID] != 3 {
		t.Fatal("технический XLSX принят за выгрузку", err)
	}
	f.badID = ""
	out, err := r.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 1)
	for _, j := range jobs {
		if j.ID != jobs[0].ID && f.calls[j.ID] != 1 {
			t.Fatal("повторена исправная выгрузка")
		}
	}
}

func TestLongPathUsesShortNamesAndNoServiceFilesInExports(t *testing.T) {
	req, local := request(t)
	req.Profile.Departments[0].Name = "Департамент с очень длинным наименованием для проверки сетевого пути"
	f := &fake{}
	r := Runner{Local: local, Factory: f.factory, Delay: time.Nanosecond}
	short := false
	out, err := r.Run(context.Background(), req, func(ev Event) {
		if ev.Plan != nil {
			short = ev.Plan.Jobs[0].Filename == "0001.xlsx"
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, out, 1)
	if !short {
		t.Fatal("не сокращены длинные имена")
	}
	entries, err := os.ReadDir(filepath.Join(out.Dir, "Исх."))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".xlsx" {
			t.Fatal("служебный файл в выгрузке", entry.Name())
		}
	}
}

type replay struct {
	output  string
	sources map[string]string
}

func (b replay) Close() {}
func (b replay) Begin(_ context.Context, j model.Job) (*queue.Pending, error) {
	if err := queue.SaveResult(b.sources[j.ID], filepath.Join(b.output, j.Filename), true); err != nil {
		return nil, err
	}
	ch := make(chan queue.Event, 1)
	ch <- queue.Event{State: queue.Saved}
	return &queue.Pending{Events: ch, Close: func() {}}, nil
}

func TestRealPipeline(t *testing.T) {
	source, exports, root := os.Getenv("FSSP_PIPELINE_SOURCE"), os.Getenv("FSSP_PIPELINE_EXPORTS"), os.Getenv("FSSP_PIPELINE_OUTPUT")
	if source == "" || exports == "" || root == "" {
		t.Skip("реальные файлы передаются переменными среды")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	copySource := filepath.Join(root, "source.xlsx")
	if err := queue.SaveResult(source, copySource, true); err != nil {
		t.Fatal(err)
	}
	p := model.Profile{ID: "real", Name: "Регион", Bases: []model.Base{{ID: "base", URL: "https://example.test"}}, Departments: []model.Department{{BaseID: "base", ID: 39, Name: "Курган"}, {BaseID: "base", ID: 43, Name: "Курган-Восточный"}, {BaseID: "base", ID: 40, Name: "Шадринск"}}}
	jobs, _ := model.BuildJobs(p)
	sources := map[string]string{}
	paths := []string{}
	for _, j := range jobs {
		sources[j.ID] = filepath.Join(exports, j.Filename)
		paths = append(paths, sources[j.ID])
	}
	req := Request{Registry: registry.Options{Source: copySource, Month: time.September, Year: 2026}, Profile: p, Parallel: 3}
	writes := 0
	r := Runner{Local: filepath.Join(root, "cache"), Delay: time.Nanosecond, Factory: func(_ context.Context, output string) (Backend, error) {
		return replay{output: output, sources: sources}, nil
	}, Save: func(src, dest string) error {
		writes++
		if writes < 3 {
			return errors.New("контрольный временный сбой записи")
		}
		return queue.SaveResult(src, dest, true)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := r.Run(ctx, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	baselineOpt := req.Registry
	baselineOpt.Exports, baselineOpt.ExportFiles = exports, paths
	baseline, err := registry.Run(ctx, baselineOpt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != baseline.Total || out.Found != baseline.Found || out.FoundByCase != baseline.FoundByCase || out.ReadRows != baseline.ReadRows || len(out.Files) != 4 {
		t.Fatalf("общий запуск отличается от отдельной сверки: %+v / %+v", out, baseline)
	}
	read := func(path string) [][]string {
		f, err := excelize.OpenFile(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		rows, err := f.GetRows(f.GetSheetList()[0])
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	for i, name := range out.Files {
		if !reflect.DeepEqual(read(filepath.Join(out.Dir, name)), read(filepath.Join(baseline.Dir, baseline.Files[i]))) {
			t.Fatal("изменилось содержимое реестра", name)
		}
	}
	t.Logf("Реальный комплект: %s; строк %d, ДР по ИД %d, по делу %d; прочитано %d; записей %d; все 4 файла совпали с отдельным запуском", out.Dir, out.Total, out.Found, out.FoundByCase, out.ReadRows, writes)
}
