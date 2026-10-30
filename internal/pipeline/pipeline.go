// Package pipeline выполняет выгрузку и сверку как один возобновляемый запуск.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/chassehiboux/FSSPReestrParser/internal/registry"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"golang.org/x/sys/windows"
)

var StageNames = append([]string{"Проверка выгрузок в папке «Исх.»"}, registry.StageNames...)

type Request struct {
	Registry registry.Options
	Profile  model.Profile
	Parallel int
	Headless bool
}

type Backend interface {
	queue.Backend
	Close()
}

type Plan struct {
	Jobs    []model.Job
	Dir     string
	Resumed bool
}
type Event struct {
	Plan   *Plan
	Export *queue.Event
	Stage  *registry.Event
}
type Result struct {
	registry.Result
	WorkDir string
}

type Runner struct {
	Local   string
	Factory func(context.Context, string) (Backend, error)
	// Save и Delay позволяют проверить сбои диска без настоящего корпоративного сервера.
	Save  func(string, string) error
	Delay time.Duration
}

type entry struct{ State, SHA256 string }
type state struct {
	Schema         int
	Signature, Dir string
	Request        Request
	Jobs           []model.Job
	Entries        map[string]entry
	Complete       bool
}
type session struct {
	state
	work    string
	lock    *os.File
	overlap windows.Overlapped
}

func digest(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 128*1024)
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		n, e := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if e == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if e != nil {
			return "", e
		}
	}
}
func valid(ctx context.Context, path, hash string) bool {
	if len(hash) != 64 || queue.ValidateXLSX(path) != nil {
		return false
	}
	actual, err := digest(ctx, path)
	return err == nil && actual == hash
}
func (s *session) save() error {
	return settings.AtomicJSON(filepath.Join(s.work, "state.json"), s.state)
}
func (s *session) close() {
	if s.lock != nil {
		windows.UnlockFileEx(windows.Handle(s.lock.Fd()), 0, 1, 0, &s.overlap)
		s.lock.Close()
	}
}
func (s *session) acquire() error {
	f, err := os.OpenFile(filepath.Join(s.work, "run.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &s.overlap)
	if err != nil {
		f.Close()
		return fmt.Errorf("этот запуск уже выполняется в другом окне программы")
	}
	s.lock = f
	return nil
}

func (r Runner) open(ctx context.Context, req Request) (*session, bool, error) {
	source, err := filepath.Abs(req.Registry.Source)
	if err != nil {
		return nil, false, err
	}
	req.Registry.Source = source
	hash, err := digest(ctx, source)
	if err != nil {
		return nil, false, err
	}
	raw, _ := json.Marshal([]any{"id-then-source-case-v1", model.Primary(), req, hash})
	sum := sha256.Sum256(raw)
	signature := hex.EncodeToString(sum[:])
	root := filepath.Join(r.Local, "pipelines")
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, false, err
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, false, err
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		work := filepath.Join(root, dir.Name())
		data, e := os.ReadFile(filepath.Join(work, "state.json"))
		var st state
		if e != nil || json.Unmarshal(data, &st) != nil || st.Schema != 1 || st.Signature != signature || st.Complete {
			continue
		}
		if !strings.EqualFold(filepath.Dir(st.Dir), filepath.Dir(source)) || !strings.HasPrefix(filepath.Base(st.Dir), "Сверка") {
			return nil, false, fmt.Errorf("некорректная папка сохранённого запуска")
		}
		s := &session{state: st, work: work}
		if err = s.acquire(); err != nil {
			return nil, false, err
		}
		// Повторное чтение под блокировкой исключает гонку с завершением другого процесса.
		data, e = os.ReadFile(filepath.Join(work, "state.json"))
		if e != nil || json.Unmarshal(data, &s.state) != nil {
			s.close()
			return nil, false, fmt.Errorf("не удалось прочитать сохранённый запуск")
		}
		if s.Complete {
			s.close()
			continue
		}
		if !valid(ctx, filepath.Join(work, "source.xlsx"), hash) {
			s.close()
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			return nil, false, fmt.Errorf("локальная копия исходника повреждена; сохранённый запуск: %s", work)
		}
		if s.Entries == nil {
			s.Entries = map[string]entry{}
		}
		return s, true, nil
	}
	jobs, err := model.BuildJobs(req.Profile)
	if err != nil {
		return nil, false, err
	}
	if len(jobs) == 0 {
		return nil, false, fmt.Errorf("нет заданий для выгрузки")
	}
	work, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return nil, false, err
	}
	s := &session{state: state{Schema: 1, Signature: signature, Request: req, Jobs: jobs, Entries: map[string]entry{}}, work: work}
	if err = s.acquire(); err != nil {
		return nil, false, err
	}
	fail := func(e error) (*session, bool, error) { s.close(); return nil, false, e }
	// Снимок исключает изменение исходника посреди длительной выгрузки.
	if err = queue.SaveResult(source, filepath.Join(work, "source.xlsx"), false); err != nil {
		return fail(err)
	}
	if !valid(ctx, filepath.Join(work, "source.xlsx"), hash) {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		return fail(fmt.Errorf("исходник изменился во время чтения; повторите запуск"))
	}
	if err = registry.CheckSource(ctx, filepath.Join(work, "source.xlsx")); err != nil {
		return fail(err)
	}
	s.Dir, err = registry.PrepareOutput(req.Registry)
	if err != nil {
		return fail(err)
	}
	// В длинных UNC-путях нужны короткие физические имена; условия остаются в таблице.
	if queue.CheckPaths(filepath.Join(s.Dir, "Исх."), s.Jobs) != nil || queue.CheckPaths(filepath.Join(work, "exports"), s.Jobs) != nil {
		for i := range s.Jobs {
			s.Jobs[i].Filename = fmt.Sprintf("%04d.xlsx", i+1)
		}
	}
	if err = queue.CheckPaths(filepath.Join(s.Dir, "Исх."), s.Jobs); err != nil {
		os.Remove(s.Dir)
		return fail(err)
	}
	if err = os.Mkdir(filepath.Join(work, "exports"), 0700); err != nil {
		os.Remove(s.Dir)
		return fail(err)
	}
	if err = s.save(); err != nil {
		os.Remove(s.Dir)
		return fail(err)
	}
	return s, false, nil
}

func (r Runner) wait(ctx context.Context, attempt int) error {
	delay := r.Delay
	if delay <= 0 {
		delay = time.Second
	}
	t := time.NewTimer(time.Duration(attempt) * delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (r Runner) Run(ctx context.Context, req Request, emit func(Event)) (out Result, err error) {
	s, resumed, err := r.open(ctx, req)
	if err != nil {
		return out, err
	}
	defer s.close()
	out.WorkDir, out.Dir = s.work, s.Dir
	if emit == nil {
		emit = func(Event) {}
	}
	emit(Event{Plan: &Plan{Jobs: s.Jobs, Dir: s.Dir, Resumed: resumed}})
	localExports := filepath.Join(s.work, "exports")
	target := filepath.Join(s.Dir, "Исх.")
	if err = os.MkdirAll(target, 0700); err != nil {
		return out, err
	}
	publish := func(name, hash string) error {
		src, dest := filepath.Join(localExports, name), filepath.Join(target, name)
		var e error
		for attempt := 1; attempt <= 3; attempt++ {
			if valid(ctx, dest, hash) {
				os.Remove(src)
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt > 1 {
				if e = r.wait(ctx, attempt-1); e != nil {
					return e
				}
			}
			if r.Save == nil {
				e = queue.SaveResult(src, dest, true)
			} else {
				e = r.Save(src, dest)
			}
			if e == nil && valid(ctx, dest, hash) {
				os.Remove(src)
				return nil
			}
			if e == nil {
				e = fmt.Errorf("проверка записанного файла не пройдена")
			}
		}
		return e
	}
	// После аварии файл мог успеть сохраниться, а его контрольная сумма — ещё нет.
	adopted := false
	for _, j := range s.Jobs {
		if s.Entries[j.ID].State != "" {
			continue
		}
		path := filepath.Join(localExports, j.Filename)
		if queue.ValidateXLSX(path) == nil && registry.CheckExport(ctx, path, req.Registry.SkipCaseRegisters) == nil {
			hash, e := digest(ctx, path)
			if e != nil {
				return out, e
			}
			s.Entries[j.ID] = entry{State: queue.Saved, SHA256: hash}
			adopted = true
		}
	}
	if adopted {
		if err = s.save(); err != nil {
			return out, err
		}
	}
	jobEvent := func(j model.Job, state, detail string) {
		emit(Event{Export: &queue.Event{JobID: j.ID, State: state, Detail: detail}})
	}
	stage := func(state, detail string, checked, total int) {
		emit(Event{Stage: &registry.Event{Stage: 0, State: state, Detail: detail, Checked: checked, Total: total}})
	}
	missing := func() []model.Job {
		pending := []model.Job{}
		for _, j := range s.Jobs {
			e := s.Entries[j.ID]
			if e.State == queue.Empty {
				jobEvent(j, queue.Empty, "Подтверждено: нет данных")
				continue
			}
			if e.State == queue.Saved && (valid(ctx, filepath.Join(localExports, j.Filename), e.SHA256) || valid(ctx, filepath.Join(target, j.Filename), e.SHA256)) {
				jobEvent(j, "Подготовлен", "Выгрузка уже скачана; "+j.Filename)
				continue
			}
			j.ReplaceExisting = true
			pending = append(pending, j)
		}
		return pending
	}
	for attempt := 1; attempt <= 3; attempt++ {
		pending := missing()
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if len(pending) == 0 {
			break
		}
		if attempt > 1 {
			if err = r.wait(ctx, attempt-1); err != nil {
				return out, err
			}
		}
		for _, j := range pending {
			jobEvent(j, queue.Queued, fmt.Sprintf("Попытка %d/3", attempt))
		}
		if r.Factory == nil {
			return out, fmt.Errorf("не настроена выгрузка")
		}
		backend, e := r.Factory(ctx, localExports)
		if e != nil {
			return out, e
		}
		var saveErr error
		var transferErr error
		queue.Run(ctx, pending, req.Parallel, backend, func(ev queue.Event) {
			if ev.State == queue.Saved {
				path := filepath.Join(localExports, jobFilename(s.Jobs, ev.JobID))
				hash, e := digest(ctx, path)
				if e == nil {
					e = queue.ValidateXLSX(path)
				}
				if e == nil {
					e = registry.CheckExport(ctx, path, req.Registry.SkipCaseRegisters)
				}
				if e != nil {
					ev.State, ev.Detail = queue.Failed, e.Error()
				} else {
					s.Entries[ev.JobID] = entry{State: queue.Saved, SHA256: hash}
					ev.State, ev.Detail = "Подготовлен", "Скачано; ожидает записи в «Исх.» · "+filepath.Base(path)
				}
			} else if ev.State == queue.Empty {
				s.Entries[ev.JobID] = entry{State: queue.Empty}
			}
			if ev.State == "Подготовлен" || ev.State == queue.Empty {
				if e := s.save(); e != nil {
					saveErr = e
				}
			}
			emit(Event{Export: &ev})
			if ev.State == "Подготовлен" && transferErr == nil && ctx.Err() == nil {
				name := jobFilename(s.Jobs, ev.JobID)
				if e := publish(name, s.Entries[ev.JobID].SHA256); e != nil {
					transferErr = e
				} else {
					event := queue.Event{JobID: ev.JobID, State: queue.Saved, Detail: "Сохранён в «Исх.»; локальная копия удалена · " + name}
					emit(Event{Export: &event})
				}
			}
		})
		backend.Close()
		if saveErr != nil {
			return out, fmt.Errorf("не удалось сохранить состояние запуска: %w", saveErr)
		}
		if transferErr != nil {
			return out, fmt.Errorf("не удалось сохранить выгрузку в «Исх.»: %w. Несохранённые локальные копии оставлены для продолжения", transferErr)
		}
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if n := len(missing()); n > 0 {
		return out, fmt.Errorf("не удалось получить выгрузки: %d. Реестры не создавались. Нажмите «Продолжить» для повтора только незавершённых заданий", n)
	}
	stage("Выполняется", "Проверка полного комплекта выгрузок…", 0, len(s.Jobs))
	if err = os.MkdirAll(target, 0700); err != nil {
		return out, err
	}
	files := []string{}
	for i, j := range s.Jobs {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		e := s.Entries[j.ID]
		dest := filepath.Join(target, j.Filename)
		if e.State == queue.Empty {
			if err = queue.ClearEmptyResult(dest, true); err != nil {
				return out, err
			}
		} else {
			var copyErr error
			for attempt := 1; attempt <= 3; attempt++ {
				if valid(ctx, dest, e.SHA256) {
					copyErr = nil
					break
				}
				if err = ctx.Err(); err != nil {
					return out, err
				}
				if attempt > 1 {
					if err = r.wait(ctx, attempt-1); err != nil {
						return out, err
					}
				}
				stage("Выполняется", fmt.Sprintf("Файл %d/%d · %s · запись %d/3", i+1, len(s.Jobs), j.Filename, attempt), i, len(s.Jobs))
				if r.Save == nil {
					copyErr = queue.SaveResult(filepath.Join(localExports, j.Filename), dest, true)
				} else {
					copyErr = r.Save(filepath.Join(localExports, j.Filename), dest)
				}
				if copyErr == nil && !valid(ctx, dest, e.SHA256) {
					copyErr = fmt.Errorf("проверка записанного файла не пройдена")
				}
				if copyErr == nil {
					break
				}
			}
			if copyErr != nil {
				stage("Ошибка", copyErr.Error(), i, len(s.Jobs))
				return out, fmt.Errorf("выгрузка скачана, но не сохранена в «Исх.»: %w. Локальная копия сохранена; можно продолжить", copyErr)
			}
			os.Remove(filepath.Join(localExports, j.Filename))
			files = append(files, dest)
			jobEvent(j, queue.Saved, "Сохранён и проверен · "+j.Filename)
		}
		stage("Выполняется", fmt.Sprintf("Проверено выгрузок %d/%d", i+1, len(s.Jobs)), i+1, len(s.Jobs))
	}
	// Барьер: повторная проверка всего комплекта перед чтением (в том числе ранее записанных файлов).
	for _, j := range s.Jobs {
		if e := s.Entries[j.ID]; e.State != queue.Empty && !valid(ctx, filepath.Join(target, j.Filename), e.SHA256) {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			return out, fmt.Errorf("выгрузка изменилась перед сверкой: %s; нажмите «Продолжить»", j.Filename)
		}
	}
	stage("Готово", fmt.Sprintf("Проверены все %d отборов; можно создавать реестры", len(s.Jobs)), len(s.Jobs), len(s.Jobs))
	opt := req.Registry
	opt.Source, opt.Exports, opt.OutputDir, opt.ExportFiles = filepath.Join(s.work, "source.xlsx"), target, s.Dir, files
	// В незавершённом запуске могли остаться собственные файлы после аварии при публикации.
	names, _ := registry.Names(opt.Month, opt.Year)
	for _, name := range names {
		if err = os.Remove(filepath.Join(s.Dir, name)); err != nil && !os.IsNotExist(err) {
			return out, err
		}
	}
	result, err := registry.Run(ctx, opt, func(ev registry.Event) { ev.Stage++; emit(Event{Stage: &ev}) })
	if err != nil {
		return out, err
	}
	out.Result = result
	s.Complete = true
	if err = s.save(); err != nil {
		return out, fmt.Errorf("реестры сохранены, но состояние завершения не записано: %w", err)
	}
	// Удаляем только известные собственные локальные копии. Журнал завершения остаётся.
	for _, j := range s.Jobs {
		os.Remove(filepath.Join(localExports, j.Filename))
	}
	os.Remove(filepath.Join(s.work, "source.xlsx"))
	return out, nil
}

func jobFilename(jobs []model.Job, id string) string {
	for _, j := range jobs {
		if j.ID == id {
			return j.Filename
		}
	}
	return ""
}

// IsCanceled отличает остановку пользователем от ошибки, допускающей продолжение.
func IsCanceled(err error) bool { return errors.Is(err, context.Canceled) }
