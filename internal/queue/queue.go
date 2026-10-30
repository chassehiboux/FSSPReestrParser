// Package queue управляет заданиями до завершения сохранения, скачиванием и конечными состояниями.
package queue

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
)

const (
	Queued      = "В очереди"
	Preparing   = "Подготовка"
	Generating  = "Формирование"
	Downloading = "Скачивание"
	Saved       = "Сохранён"
	Empty       = "Нет данных"
	Failed      = "Ошибка"
	Canceled    = "Отменён"
)

type Event struct {
	RetryAuth bool
	JobID     string
	State     string
	Detail    string
	Bytes     int64
	Total     int64
	Path      string
	At        time.Time
}
type Pending struct {
	Events <-chan Event
	Close  func()
}
type Backend interface {
	Begin(context.Context, model.Job) (*Pending, error)
}

// ErrAuthRequired разрешает повтор подготовки только при подтверждённом возврате на вход.
var ErrAuthRequired = errors.New("Требуется восстановить авторизацию")

type Sink func(Event)

func Run(ctx context.Context, jobs []model.Job, parallel int, backend Backend, emit Sink) {
	if parallel < 1 {
		parallel = 1
	}
	var mu sync.Mutex
	send := func(e Event) { mu.Lock(); defer mu.Unlock(); e.At = time.Now(); emit(e) }
	for _, job := range jobs {
		send(Event{JobID: job.ID, State: Queued})
	}
	work := make(chan model.Job)
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				if ctx.Err() != nil {
					send(Event{JobID: job.ID, State: Canceled})
					continue
				}
				runJob(ctx, job, backend, send)
			}
		}()
	}
	for _, job := range jobs {
		work <- job
	}
	close(work)
	wg.Wait()
}
func runJob(ctx context.Context, job model.Job, backend Backend, send Sink) {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		message := ""
		if attempt > 0 {
			message = "Восстановление входа и повтор задания…"
		}
		send(Event{JobID: job.ID, State: Preparing, Detail: message})
		p, e := backend.Begin(ctx, job)
		if e != nil {
			if attempt == 0 && ctx.Err() == nil && errors.Is(e, ErrAuthRequired) {
				continue
			}
			state := Failed
			if ctx.Err() != nil {
				if ctx.Err() == context.Canceled {
					state = Canceled
				}
			}
			detail := strings.TrimPrefix(e.Error(), "eval js error: Error: ")
			detail = strings.SplitN(detail, "\n", 2)[0]
			if state == Canceled {
				detail = "Операция остановлена пользователем"
			}
			send(Event{JobID: job.ID, State: state, Detail: detail})
			return
		}
		retry := false
		for {
			select {
			case <-ctx.Done():
				p.Close()
				state := Canceled
				if ctx.Err() == context.DeadlineExceeded {
					state = Failed
				}
				send(Event{JobID: job.ID, State: state, Detail: "Ожидание завершено: " + ctx.Err().Error()})
				return
			case e, ok := <-p.Events:
				if !ok {
					p.Close()
					send(Event{JobID: job.ID, State: Failed, Detail: "Обработчик скачивания завершился без результата"})
					return
				}
				if e.RetryAuth && attempt == 0 {
					p.Close()
					retry = true
					break
				}
				e.JobID = job.ID
				send(e)
				if Terminal(e.State) {
					p.Close()
					return
				}
			}
			if retry {
				break
			}
		}
	}
}
func Terminal(s string) bool { return s == Saved || s == Empty || s == Failed || s == Canceled }

// ValidateXLSX проверяет структуру книги и целостность записей ZIP перед сохранением.
func ValidateXLSX(source string) error {
	r, e := zip.OpenReader(source)
	if e != nil {
		return fmt.Errorf("Сервер вернул не XLSX: %w", e)
	}
	defer r.Close()
	content, workbook := false, false
	for _, f := range r.File {
		content = content || f.Name == "[Content_Types].xml"
		workbook = workbook || f.Name == "xl/workbook.xml"
		if !f.FileInfo().IsDir() {
			stream, err := f.Open()
			if err != nil {
				return err
			}
			_, err = io.Copy(io.Discard, stream)
			stream.Close()
			if err != nil {
				return fmt.Errorf("Повреждена книга Excel: %w", err)
			}
		}
	}
	if !content || !workbook {
		return fmt.Errorf("В скачанном файле отсутствует структура книги Excel")
	}
	return nil
}
func SaveXLSX(source, target string) error { return SaveResult(source, target, false) }
func SaveResult(source, target string, replace bool) error {
	if err := ValidateXLSX(source); err != nil {
		return err
	}
	// Сначала копируем во временный файл на том же диске, сохраняя предыдущий результат.
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	f, e := os.CreateTemp(filepath.Dir(target), ".fssp-*.part")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, e = in.WriteTo(f); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	// Замена разрешена только выбранным режимом; иначе существующий результат не перезаписывается.
	if replace {
		e = os.Rename(temp, target)
	} else {
		e = os.Link(temp, target)
	}
	if e != nil {
		return fmt.Errorf("Не удалось сохранить %s: %w", filepath.Base(target), e)
	}
	return nil
}

func Existing(dir string, jobs []model.Job) (present, missing []model.Job) {
	for _, j := range jobs {
		if ValidateXLSX(filepath.Join(dir, j.Filename)) == nil {
			present = append(present, j)
		} else {
			missing = append(missing, j)
		}
	}
	return
}

// Подтверждённый пустой отбор не должен оставлять устаревшие строки для будущих реестров.
func ClearEmptyResult(target string, replace bool) error {
	if !replace {
		return nil
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Не удалось убрать прежний результат: путь занят не обычным файлом")
	}
	return os.Remove(target)
}
func CheckPaths(dir string, jobs []model.Job) error {
	for _, j := range jobs {
		p := filepath.Join(dir, j.Filename)
		if len([]rune(p)) > 240 {
			return fmt.Errorf("Путь слишком длинный: %s", j.Filename)
		}
		if !strings.EqualFold(filepath.Ext(j.Filename), ".xlsx") || filepath.Base(j.Filename) != j.Filename {
			return fmt.Errorf("Некорректное имя файла")
		}
	}
	return nil
}
