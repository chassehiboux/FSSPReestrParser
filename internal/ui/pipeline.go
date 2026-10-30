package ui

import (
	"context"
	"fmt"

	"github.com/chassehiboux/FSSPReestrParser/internal/browser"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/pipeline"
	"github.com/chassehiboux/FSSPReestrParser/internal/platform"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/chassehiboux/FSSPReestrParser/internal/registry"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"github.com/lxn/walk"
)

func (a *App) startPipeline() {
	if a.running {
		if a.pipeline != nil && a.pipeline.running {
			a.showOperation(a.pipeline)
		}
		return
	}
	if a.updating {
		return
	}
	if a.pendingUpdate != nil {
		a.runPendingUpdate()
		return
	}
	source, accepted, err := platform.ChooseSourceFile(uintptr(a.window.Handle()), "Выберите исходный файл для выгрузки и создания реестров")
	if err != nil {
		a.error(err)
		return
	}
	if !accepted {
		return
	}
	opt, accepted, err := chooseRegisters(a.window, source)
	if err != nil {
		a.error(err)
		return
	}
	if !accepted {
		return
	}
	selected := ""
	if i := a.profiles.CurrentIndex(); i >= 0 && i < len(a.cfg.Profiles) {
		selected = a.cfg.Profiles[i].ID
	}
	dir := ""
	op, err := newOperation(a, "Выгрузка ОВЗИД и создание реестров", true, func() {
		if dir != "" {
			if e := platform.OpenFolder(dir); e != nil {
				a.error(e)
			}
		}
	}, true)
	if err != nil {
		a.error(err)
		return
	}
	a.pipeline = op
	op.model.mixed, op.model.stageNames = true, pipeline.StageNames
	var snapshot *pipeline.Request
	op.retryFunc = func() {
		if a.running || a.updating {
			return
		}
		op.running, a.running = true, true
		op.retry.SetVisible(false)
		op.stop.SetEnabled(true)
		op.progress.SetValue(0)
		op.model.stages = nil
		for i := range pipeline.StageNames {
			op.model.stages = append(op.model.stages, registry.Event{Stage: i, State: "Ожидание"})
		}
		initial := registry.Options{SkipAnalytics: a.cfg.SkipAnalytics, SkipCaseRegisters: a.cfg.SkipCaseRegisters}
		if snapshot != nil {
			initial = snapshot.Registry
		}
		op.model.filterStages(initial, 1)
		op.model.PublishRowsReset()
		a.showOperation(op)
		op.status.SetText("Подготовка общего запуска…")
		a.status.SetText("Выгрузка и создание реестров выполняются…")
		ctx, cancel := context.WithCancel(a.lifecycle)
		a.cancel, op.cancel = cancel, cancel
		a.enable()
		go func() {
			defer cancel()
			finish := func(result pipeline.Result, runErr error) {
				a.window.Synchronize(func() {
					op.running, a.running = false, false
					a.cancel, op.cancel = nil, nil
					op.stop.SetEnabled(false)
					if runErr != nil {
						canceled := pipeline.IsCanceled(runErr)
						for i, e := range op.model.stages {
							if e.State == "Ожидание" || e.State == "Выполняется" || e.State == "Подготовлен" {
								state := "Не выполнен"
								if canceled {
									state = "Отменён"
								} else if e.State == "Выполняется" {
									state = "Ошибка"
								}
								op.model.stages[i].State = state
							}
						}
						op.model.PublishRowsReset()
						op.status.SetText(runErr.Error())
						a.status.SetText("Запуск не завершён. Готовые выгрузки сохранены; можно продолжить.")
						op.retry.SetVisible(true)
						a.notify("Выгрузка и реестры не завершены", "Готовые выгрузки сохранены. Нажмите «Продолжить» в программе.")
						if !canceled && !a.closing {
							a.error(runErr)
						}
					} else {
						op.progress.SetValue(100)
						message := fmt.Sprintf("Готово. Файлов реестров: %d. Проверено %d/%d. %s.", len(result.Files), result.Total, result.Total, registry.BirthSummary(result.Found, result.FoundByCase, snapshot.Registry.SkipCaseRegisters))
						op.status.SetText(message)
						a.status.SetText("Выгрузка и реестры созданы")
						a.notify("Выгрузка и реестры созданы", message)
						if err := platform.OpenFolder(result.Dir); err != nil {
							a.error(err)
						}
					}
					a.enable()
					if a.closing {
						a.window.Close()
					}
				})
			}
			if snapshot == nil {
				cfg, warning, err := a.store.Load()
				if err != nil {
					finish(pipeline.Result{}, err)
					return
				}
				var profile model.Profile
				for _, p := range cfg.Profiles {
					if p.ID == selected {
						profile = p
						break
					}
				}
				if profile.ID == "" {
					finish(pipeline.Result{}, fmt.Errorf("регион изменился в настройках; перезапустите программу"))
					return
				}
				opt.SkipAnalytics, opt.SkipCaseRegisters = cfg.SkipAnalytics, cfg.SkipCaseRegisters
				snapshot = &pipeline.Request{Registry: opt, Profile: profile, Parallel: cfg.Parallel, Headless: cfg.Headless}
				a.window.Synchronize(func() {
					a.cfg = cfg
					if warning != "" {
						walk.MsgBox(a.window, "Настройки общего запуска", warning, walk.MsgBoxIconWarning)
					}
				})
			}
			runner := pipeline.Runner{Local: a.local, Factory: func(ctx context.Context, output string) (pipeline.Backend, error) {
				engine, err := browser.New(ctx, a.local, output, snapshot.Headless, a.prompt)
				if err == nil {
					engine.SetSharedCatalog(settings.CatalogRoot(a.store.Shared))
				}
				return engine, err
			}}
			result, err := runner.Run(ctx, *snapshot, func(ev pipeline.Event) {
				a.window.Synchronize(func() {
					if ev.Plan != nil {
						op.model.filterStages(snapshot.Registry, 1)
						dir = ev.Plan.Dir
						op.model.jobs, op.model.events = ev.Plan.Jobs, map[string]queue.Event{}
						for _, j := range ev.Plan.Jobs {
							op.model.events[j.ID] = queue.Event{JobID: j.ID, State: queue.Queued}
						}
						op.model.PublishRowsReset()
						if ev.Plan.Resumed {
							op.status.SetText("Продолжение сохранённого запуска…")
						}
					} else if ev.Export != nil {
						e := *ev.Export
						op.model.events[e.JobID] = e
						for i, j := range op.model.jobs {
							if j.ID == e.JobID {
								op.model.PublishRowChanged(i)
								break
							}
						}
						done := 0
						for _, e := range op.model.events {
							if e.State == "Подготовлен" || e.State == queue.Saved || e.State == queue.Empty {
								done++
							}
						}
						if len(op.model.jobs) > 0 {
							op.progress.SetValue(max(op.progress.Value(), done*60/len(op.model.jobs)))
						}
						op.status.SetText(fmt.Sprintf("Выгрузки: готово %d/%d · %s", done, len(op.model.jobs), e.Detail))
					} else if ev.Stage != nil {
						e := *ev.Stage
						op.model.stages[e.Stage] = e
						row := op.model.stageRow(e.Stage)
						if row < 0 {
							return
						}
						op.model.PublishRowChanged(len(op.model.jobs) + row)
						progress := 60
						if e.Stage == 0 && e.Total > 0 {
							progress += e.Checked * 10 / e.Total
						} else if e.Stage > 0 {
							progress = 70 + (row-1)*30/(op.model.stageCount()-1)
						}
						op.progress.SetValue(max(op.progress.Value(), progress))
						op.status.SetText(e.Detail)
					}
				})
			})
			finish(result, err)
		}()
	}
	op.retryFunc()
}
