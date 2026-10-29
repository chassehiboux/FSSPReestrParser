// verify проверяет настоящий движок без GUI и не выводит учётные данные.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/browser"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
)

func main() {
	defer func() {
		if value := recover(); value != nil {
			if err, ok := value.(error); ok {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			panic(value)
		}
	}()
	config := flag.String("settings", "", "Файл настроек")
	state := flag.String("state", "", "Локальное состояние браузера для проверки")
	out := flag.String("downloads", "", "Корень контрольной выгрузки")
	doExport := flag.Bool("export", false, "Выполнить выгрузку основного блока")
	diagnose := flag.Bool("diagnose", false, "Проверить сетевые ошибки и состояние страницы без выгрузки")
	headful := flag.Bool("headful", false, "Показать браузер при проверке")
	department := flag.Int("department", 0, "Ограничить диагностическую проверку департаментом")
	limit := flag.Int("limit", 0, "Ограничить число контрольных заданий")
	perDepartment := flag.Int("per-department", 0, "Число контрольных заданий на департамент")
	mode := flag.String("mode", "missing", "Режим: missing или all")
	catalogRoot := flag.String("catalog-root", "", "Общая папка каталога полей")
	refreshAll := flag.Bool("refresh-all", false, "Обновить все настроенные базы и департаменты")
	flag.Parse()
	if *config == "" {
		panic("Укажите -settings")
	}
	s, err := settings.Read(*config)
	must(err)
	p := s.Profiles[0]
	if *department != 0 {
		selected := []model.Department{}
		for _, d := range p.Departments {
			if d.ID == *department {
				selected = append(selected, d)
			}
		}
		if len(selected) == 0 {
			panic("Департамент отсутствует")
		}
		p.Departments = selected
	}
	if *headful {
		s.Headless = false
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	local := filepath.Join(filepath.Dir(*config), "verification")
	must(os.MkdirAll(local, 0700))
	dir := local
	if *doExport {
		if *out == "" {
			panic("Укажите -downloads")
		}
		dir, err = model.NewRunDir(*out, time.Now())
		must(err)
	}
	e, err := browser.New(ctx, local, dir, s.Headless, nil)
	must(err)
	defer e.Close()
	if *catalogRoot != "" {
		e.SetSharedCatalog(*catalogRoot)
	}
	if *state != "" {
		must(e.ImportState(*state))
	}
	if *refreshAll {
		_, err = e.RefreshAll(ctx, s.Profiles)
		must(err)
		fmt.Println("Кэш обновлён для всех настроенных баз и департаментов")
		return
	}
	if *diagnose {
		result := e.Diagnose(ctx, p)
		raw, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(raw))
		return
	}
	catalog, err := e.Refresh(ctx, p)
	must(err)
	for _, d := range p.Departments {
		fmt.Println(d.Name, "полей:", len(catalog[model.Scope(d)]))
	}
	if !*doExport {
		return
	}
	jobs, err := model.BuildJobs(p)
	must(err)
	if *perDepartment > 0 {
		counts := map[string]int{}
		selected := []model.Job{}
		for _, j := range jobs {
			key := model.Scope(j.Department)
			if counts[key] < *perDepartment {
				selected = append(selected, j)
				counts[key]++
			}
		}
		jobs = selected
	}
	if *limit > 0 && len(jobs) > *limit {
		jobs = jobs[:*limit]
	}
	if *mode == "missing" {
		_, jobs = queue.Existing(dir, jobs)
	} else if *mode != "all" {
		panic("Неизвестный режим")
	}
	for i := range jobs {
		jobs[i].ReplaceExisting = true
	}
	must(queue.CheckPaths(dir, jobs))
	last := map[string]string{}
	failed := false
	queue.Run(ctx, jobs, s.Parallel, e, func(ev queue.Event) {
		if ev.State != last[ev.JobID] {
			fmt.Println(ev.At.Format("15:04:05.000"), ev.JobID[:8], ev.State, ev.Detail)
			last[ev.JobID] = ev.State
		}
		if ev.State == queue.Failed {
			failed = true
		}
	})
	fmt.Println("Папка:", dir)
	if failed {
		panic(fmt.Errorf("Контрольная выгрузка завершилась с ошибками"))
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
