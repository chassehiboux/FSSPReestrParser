package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/browser"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/platform"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/chassehiboux/FSSPReestrParser/internal/registry"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"github.com/chassehiboux/FSSPReestrParser/internal/update"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type statusModel struct {
	walk.TableModelBase
	jobs         []model.Job
	events       map[string]queue.Event
	stages       []registry.Event
	mixed        bool
	stageNames   []string
	hiddenStages map[int]bool
}

func (m *statusModel) filterStages(opt registry.Options, offset int) {
	m.hiddenStages = map[int]bool{}
	if opt.SkipAnalytics {
		m.hiddenStages[4+offset] = true
	}
	if opt.SkipCaseRegisters {
		m.hiddenStages[5+offset] = true
		m.hiddenStages[6+offset] = true
	}
}
func (m *statusModel) stageRow(stage int) int {
	if m.hiddenStages[stage] {
		return -1
	}
	row := 0
	for i := 0; i < stage; i++ {
		if !m.hiddenStages[i] {
			row++
		}
	}
	return row
}
func (m *statusModel) stageIndex(row int) int {
	for i := range m.stages {
		if !m.hiddenStages[i] {
			if row == 0 {
				return i
			}
			row--
		}
	}
	return -1
}
func (m *statusModel) stageCount() int {
	n := 0
	for i := range m.stages {
		if !m.hiddenStages[i] {
			n++
		}
	}
	return n
}

func (m *statusModel) RowCount() int {
	if m.mixed {
		return len(m.jobs) + m.stageCount()
	}
	if m.stages != nil {
		return m.stageCount()
	}
	return len(m.jobs)
}
func (m *statusModel) Value(row, col int) any {
	if m.mixed {
		if row < len(m.jobs) {
			j := m.jobs[row]
			e := m.events[j.ID]
			parts := []string{}
			for _, c := range j.Conditions {
				parts = append(parts, c.Label)
			}
			switch col {
			case 0:
				return row + 1
			case 1:
				return "Выгрузка: " + j.Department.Name + " · " + strings.Join(parts, " И ")
			case 2:
				return e.State
			case 3:
				if e.State == queue.Downloading && e.Total > 0 {
					return fmt.Sprintf("%.1f / %.1f МБ", float64(e.Bytes)/1048576, float64(e.Total)/1048576)
				}
				return e.Detail
			}
			return ""
		}
		i := m.stageIndex(row - len(m.jobs))
		e := m.stages[i]
		switch col {
		case 0:
			return row + 1
		case 1:
			return m.stageNames[i]
		case 2:
			return e.State
		case 3:
			return e.Detail
		}
		return ""
	}
	if m.stages != nil {
		i := m.stageIndex(row)
		e := m.stages[i]
		switch col {
		case 0:
			return row + 1
		case 1:
			return registry.StageNames[i]
		case 2:
			return e.State
		case 3:
			return e.Detail
		}
		return ""
	}
	j := m.jobs[row]
	ev := m.events[j.ID]
	switch col {
	case 0:
		return row + 1
	case 1:
		return j.Department.Name
	case 2:
		parts := []string{}
		for _, c := range j.Conditions {
			parts = append(parts, c.Label)
		}
		return strings.Join(parts, " И ")
	case 3:
		return ev.State
	case 4:
		if ev.State == queue.Downloading && ev.Total > 0 {
			return fmt.Sprintf("%.1f / %.1f МБ", float64(ev.Bytes)/1048576, float64(ev.Total)/1048576)
		}
		return ev.Detail
	}
	return ""
}

func (m *statusModel) stageState(row int) string {
	if row < 0 {
		return ""
	}
	if m.mixed {
		if row < len(m.jobs) {
			return m.events[m.jobs[row].ID].State
		}
		row -= len(m.jobs)
	}
	if i := m.stageIndex(row); i >= 0 {
		return m.stages[i].State
	}
	return ""
}

type App struct {
	window             *walk.MainWindow
	start, admin, stop *walk.PushButton
	status             *walk.Label
	progress           *walk.ProgressBar
	profiles           *walk.ComboBox
	table              *walk.TableView
	store              *settings.Store
	cfg                model.Settings
	local, output      string
	model              *statusModel
	cancel             context.CancelFunc
	running            bool
	closing            bool
	owner              walk.Form
	skipUpdate         bool
	ready              string
	jobStatus          *walk.Label
	export             *operation
	host               *walk.Composite
	operations         []*operation
	register           *operation
	notifications      *walk.NotifyIcon
	updateButton       *walk.PushButton
	pendingUpdate      *update.Release
	updating           bool
	lifecycle          context.Context
	lifecycleCancel    context.CancelFunc
	registerButton     *walk.PushButton
	registerOutput     string
	initializing       bool
	pipelineButton     *walk.PushButton
	pipeline           *operation
}

func Run(store *settings.Store, local, ready string, skipUpdate bool) error {
	a := &App{store: store, local: local, skipUpdate: skipUpdate, ready: ready, initializing: true}
	a.lifecycle, a.lifecycleCancel = context.WithCancel(context.Background())
	defer a.lifecycleCancel()
	var separate *walk.Composite
	var more *walk.PushButton
	err := (MainWindow{AssignTo: &a.window, Title: "Реестры ФССП", Icon: appIcon(), MinSize: Size{Width: 360, Height: 330}, Size: Size{Width: 380, Height: 360}, Font: Font{Family: "Segoe UI", PointSize: 10}, Layout: VBox{Margins: Margins{Left: 24, Top: 20, Right: 24, Bottom: 20}, Spacing: 14}, Children: []Widget{
		Label{Text: "Реестры ФССП", Font: Font{Family: "Segoe UI", PointSize: 20, Bold: true}},
		Label{Text: "Выберите исходный файл и период."},
		ComboBox{AssignTo: &a.profiles, Visible: false},
		PushButton{AssignTo: &a.pipelineButton, Text: "Выгрузить ОВЗИД и создать реестры", Enabled: false, MinSize: Size{Width: 300, Height: 48}, OnClicked: a.startPipeline},
		PushButton{AssignTo: &more, Text: "Отдельные операции ▸", OnClicked: func() {
			visible := !separate.Visible()
			separate.SetVisible(visible)
			if visible {
				more.SetText("Отдельные операции ▾")
			} else {
				more.SetText("Отдельные операции ▸")
			}
			size := a.window.Size()
			if visible {
				size.Height += 110
			} else {
				size.Height -= 110
			}
			a.window.SetSize(size)
		}},
		Composite{AssignTo: &separate, Visible: false, Layout: VBox{Spacing: 8}, Children: []Widget{
			PushButton{AssignTo: &a.start, Text: "Только выгрузить ОВЗИД", Enabled: false, MinSize: Size{Width: 300, Height: 40}, OnClicked: a.startExport},
			PushButton{AssignTo: &a.registerButton, Text: "Только создать реестры", Enabled: false, MinSize: Size{Width: 300, Height: 40}, OnClicked: a.startRegisters},
		}},
		Label{AssignTo: &a.status, Text: "Проверка обновлений…", MaxSize: Size{Width: 320}},
		Composite{AssignTo: &a.host, Visible: false, StretchFactor: 1, Layout: VBox{}},
		Composite{Layout: HBox{}, Children: []Widget{PushButton{AssignTo: &a.admin, Text: "Администрирование", Visible: platform.IsAdmin(), Enabled: false, OnClicked: a.openAdmin}, PushButton{AssignTo: &a.updateButton, Text: "Обновить", Visible: false, OnClicked: a.runPendingUpdate}, HSpacer{}, Label{Text: "Версия " + update.Version}}},
	}}).Create()
	if err != nil {
		return err
	}
	a.owner = a.window
	a.setupNotifications()
	if a.notifications != nil {
		defer a.notifications.Dispose()
	}
	a.window.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if a.running {
			if walk.MsgBox(a.window, "Завершение работы", "Остановить текущую операцию и закрыть приложение?", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
				*canceled = true
				return
			}
			if a.cancel != nil {
				a.cancel()
			}
			a.closing = true
			*canceled = true
		}
	})
	if ready != "" {
		os.WriteFile(ready, []byte(update.Version), 0600)
	}
	go a.initialize()
	go a.backgroundUpdates()
	a.window.Run()
	return nil
}
func (a *App) openOutput() {
	dir := a.output
	if dir == "" {
		root, err := platform.Downloads()
		if err != nil {
			a.error(err)
			return
		}
		dir = filepath.Join(root, "Выгрузка ОВЗИД для реестров ФССП")
		os.MkdirAll(dir, 0700)
	}
	if err := platform.OpenFolder(dir); err != nil {
		a.error(err)
	}
}
func (a *App) error(e error) {
	walk.MsgBox(a.window, "Не удалось выполнить операцию", e.Error(), walk.MsgBoxIconError)
}
func (a *App) enable() {
	a.pipelineButton.SetEnabled(!a.initializing && len(a.cfg.Profiles) > 0 && !a.updating && (!a.running || a.pipeline != nil && a.pipeline.running))
	a.start.SetEnabled(len(a.cfg.Profiles) > 0 && !a.updating && (!a.running || a.export != nil && a.export.running))
	a.registerButton.SetEnabled(!a.initializing && !a.updating && (!a.running || a.register != nil && a.register.running))
	a.admin.SetEnabled(platform.IsAdmin() && len(a.cfg.Profiles) > 0 && !a.running && !a.updating)
	a.updateButton.SetEnabled(a.pendingUpdate != nil && !a.running && !a.updating)
	if a.stop != nil {
		a.stop.SetEnabled(a.export != nil && a.export.running)
	}
}
func (a *App) initialize() {
	if !a.skipUpdate && !a.startupUpdates() {
		return
	}
	cfg, warning, err := a.store.Load()
	a.window.Synchronize(func() {
		a.initializing = false
		a.enable()
		if err != nil {
			a.status.SetText("Общие настройки не загружены")
			a.error(err)
			return
		}
		a.cfg = cfg
		names := []string{}
		for _, p := range cfg.Profiles {
			names = append(names, p.Name)
		}
		a.profiles.SetModel(names)
		a.profiles.SetCurrentIndex(0)
		a.profiles.SetVisible(len(names) > 1)
		a.status.SetText("Готово к выгрузке. Настройки версии " + fmt.Sprint(cfg.Revision))
		a.enable()
		if warning != "" {
			walk.MsgBox(a.window, "Используются сохранённые настройки", warning, walk.MsgBoxIconWarning)
		}
	})
}
func (a *App) prompt(ctx context.Context, message, username string) (browser.Credentials, error) {
	type response struct {
		credentials browser.Credentials
		err         error
	}
	ch := make(chan response, 1)
	a.window.Synchronize(func() {
		if ctx.Err() != nil {
			ch <- response{err: ctx.Err()}
			return
		}
		var dlg *walk.Dialog
		var login, password *walk.LineEdit
		var remember *walk.CheckBox
		var ok, cancel *walk.PushButton
		var result browser.Credentials
		code, err := (Dialog{AssignTo: &dlg, Title: "Вход в Пирамиду", MinSize: Size{Width: 560, Height: 330}, DefaultButton: &ok, CancelButton: &cancel, Layout: VBox{}, Children: []Widget{Label{Text: message}, Label{Text: "Введите те же логин и пароль, что и при входе в учётную запись Windows.\nЛогин указывайте без vostok\\ — только имя пользователя."}, Label{Text: "Логин"}, LineEdit{AssignTo: &login, Text: username}, Label{Text: "Пароль"}, LineEdit{AssignTo: &password, PasswordMode: true}, CheckBox{AssignTo: &remember, Text: "Запомнить на этом компьютере"}, Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "Войти", OnClicked: func() {
			if strings.TrimSpace(login.Text()) == "" || password.Text() == "" {
				walk.MsgBox(dlg, "Вход", "Введите логин и пароль", walk.MsgBoxIconWarning)
				return
			}
			result = browser.Credentials{Username: strings.TrimSpace(login.Text()), Password: password.Text(), Remember: remember.Checked()}
			dlg.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}}}}).Run(a.owner)
		if err == nil && code != walk.DlgCmdOK {
			err = context.Canceled
			if a.cancel != nil {
				a.cancel()
			}
		}
		ch <- response{result, err}
	})
	select {
	case <-ctx.Done():
		return browser.Credentials{}, ctx.Err()
	case r := <-ch:
		return r.credentials, r.err
	}
}
func (a *App) startExport() {
	if a.running {
		if a.export != nil {
			a.showOperation(a.export)
		}
		return
	}
	if a.pendingUpdate != nil {
		a.runPendingUpdate()
		return
	}
	op, err := newOperation(a, "Выгрузка ОВЗИД", true, a.openOutput)
	if err != nil {
		a.error(err)
		return
	}
	a.export = op
	a.model = op.model
	a.table = op.table
	a.progress = op.progress
	a.stop = op.stop
	a.jobStatus = op.status
	op.running = true
	a.running = true
	a.status.SetText("Выгрузка выполняется…")
	a.enable()
	a.jobStatus.SetText("Получение общих настроек…")
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	op.cancel = cancel
	selected := ""
	if i := a.profiles.CurrentIndex(); i >= 0 && i < len(a.cfg.Profiles) {
		selected = a.cfg.Profiles[i].ID
	}
	go func() {
		cfg, warning, err := a.store.Load()
		if err != nil {
			a.finish(err)
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
			a.finish(errors.New("Регион изменился в общих настройках. Перезапустите приложение"))
			return
		}
		jobs, err := model.BuildJobs(profile)
		if err != nil {
			a.finish(err)
			return
		}
		root, err := platform.Downloads()
		if err != nil {
			a.finish(err)
			return
		}
		dir, existed, err := model.DayDir(root, time.Now())
		if err != nil {
			a.finish(err)
			return
		}
		if err = queue.CheckPaths(dir, jobs); err != nil {
			a.finish(err)
			return
		}
		present, missing := queue.Existing(dir, jobs)
		toRun := jobs
		mode := resumeMissing
		if existed {
			type answer struct {
				mode int
				err  error
			}
			response := make(chan answer, 1)
			a.window.Synchronize(func() { m, e := chooseResume(a.window, len(present), len(jobs), missing); response <- answer{m, e} })
			select {
			case <-ctx.Done():
				a.abortStart("Выгрузка отменена")
				return
			case answer := <-response:
				if answer.err != nil {
					a.finish(answer.err)
					return
				}
				mode = answer.mode
			}
			if mode == resumeCancel {
				cancel()
				a.abortStart("Выгрузка отменена")
				return
			}
		}
		if mode == resumeMissing {
			toRun = missing
		}
		for i := range toRun {
			toRun[i].ReplaceExisting = true
		}
		ready := make(chan struct{})
		a.window.Synchronize(func() {
			a.cfg = cfg
			a.output = dir
			a.model.jobs = jobs
			a.model.events = map[string]queue.Event{}
			if mode == resumeMissing {
				for _, job := range present {
					a.model.events[job.ID] = queue.Event{JobID: job.ID, State: queue.Saved, Detail: "Файл уже есть, пропущен"}
				}
			}
			a.model.PublishRowsReset()
			a.progress.SetValue(len(a.model.events) * 100 / len(jobs))
			a.jobStatus.SetText(fmt.Sprintf("Подготовка %d заданий…", len(toRun)))
			if warning != "" {
				walk.MsgBox(a.window, "Общие настройки", warning, walk.MsgBoxIconWarning)
			}
			close(ready)
		})
		<-ready
		if len(toRun) == 0 {
			a.finish(nil)
			return
		}
		engine, err := browser.New(ctx, a.local, dir, cfg.Headless, a.prompt)
		if err != nil {
			a.finish(err)
			return
		}
		engine.SetSharedCatalog(settings.CatalogRoot(a.store.Shared))
		started := time.Now()
		queue.Run(ctx, toRun, cfg.Parallel, engine, func(event queue.Event) {
			a.window.Synchronize(func() {
				a.model.events[event.JobID] = event
				done, failed := 0, 0
				for _, ev := range a.model.events {
					if queue.Terminal(ev.State) {
						done++
					}
					if ev.State == queue.Failed {
						failed++
					}
				}
				for i, j := range jobs {
					if j.ID == event.JobID {
						a.model.PublishRowChanged(i)
						break
					}
				}
				a.progress.SetValue(done * 100 / len(jobs))
				a.jobStatus.SetText(fmt.Sprintf("Завершено %d из %d · ошибок %d · %s", done, len(jobs), failed, time.Since(started).Round(time.Second)))
			})
		})
		engine.Close()
		a.finish(nil)
	}()
}
func (a *App) finish(err error) {
	a.window.Synchronize(func() {
		a.running = false
		a.cancel = nil
		if a.export != nil {
			a.export.running = false
		}
		a.enable()
		if a.closing {
			a.window.Close()
			return
		}
		if err != nil {
			a.jobStatus.SetText("Операция не выполнена")
			if errors.Is(err, context.Canceled) {
				a.jobStatus.SetText("Выгрузка остановлена")
				a.notify("Выгрузка остановлена", "Операция отменена пользователем.")
				return
			}
			a.error(err)
			return
		}
		saved, empty, failed, canceled := 0, 0, 0, 0
		for _, ev := range a.model.events {
			switch ev.State {
			case queue.Saved:
				saved++
			case queue.Empty:
				empty++
			case queue.Failed:
				failed++
			case queue.Canceled:
				canceled++
			}
		}
		a.jobStatus.SetText(fmt.Sprintf("Сохранено: %d · нет данных: %d · ошибок: %d · отменено: %d", saved, empty, failed, canceled))
		a.status.SetText("Готово к следующей выгрузке")
		a.notifyCompletion(saved, len(a.model.jobs), failed, empty, canceled)
		if a.pendingUpdate != nil {
			a.status.SetText("Доступно обновление " + a.pendingUpdate.Version)
		}
	})
}
func cloneSettings(s model.Settings) model.Settings {
	raw, _ := json.Marshal(s)
	var out model.Settings
	json.Unmarshal(raw, &out)
	return out
}
