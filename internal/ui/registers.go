package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/platform"
	"github.com/chassehiboux/FSSPReestrParser/internal/registry"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

func chooseRegisters(owner walk.Form) (registry.Options, bool, error) {
	var dlg *walk.Dialog
	var source, exports *walk.LineEdit
	var year *walk.NumberEdit
	var ok, cancel *walk.PushButton
	var monthButtons [12]*walk.PushButton
	var selectedPeriod *walk.Label
	now := time.Now()
	month := now.Month()
	var result registry.Options
	validate := func() {
		if ok == nil || source == nil || exports == nil {
			return
		}
		s, se := os.Stat(source.Text())
		d, de := os.Stat(exports.Text())
		ok.SetEnabled(se == nil && !s.IsDir() && strings.EqualFold(filepath.Ext(source.Text()), ".xlsx") && de == nil && d.IsDir())
	}
	monthNames := []string{"Январь", "Февраль", "Март", "Апрель", "Май", "Июнь", "Июль", "Август", "Сентябрь", "Октябрь", "Ноябрь", "Декабрь"}
	refreshPeriod := func() {
		if selectedPeriod != nil && year != nil {
			selectedPeriod.SetText(fmt.Sprintf("Выбрано: %s %d", monthNames[month-1], int(year.Value())))
		}
		for i, b := range monthButtons {
			if b != nil {
				label := monthNames[i]
				if i == int(month)-1 {
					label = "✓  " + label
				}
				b.SetText(label)
				style := uintptr(0)
				if i == int(month)-1 {
					style = 1
				}
				win.SendMessage(b.Handle(), 0xF4, style, 1) // BM_SETSTYLE: выделение выбранного месяца.
			}
		}
	}
	months := []Widget{}
	for i, name := range monthNames {
		m := time.Month(i + 1)
		months = append(months, PushButton{AssignTo: &monthButtons[i], Text: name, MinSize: Size{Width: 158, Height: 44}, Row: i / 4, Column: i % 4, OnClicked: func() { month = m; refreshPeriod() }})
	}
	err := (Dialog{AssignTo: &dlg, Title: "Создание реестров ФССП", Icon: appIcon(), Font: Font{Family: "Segoe UI", PointSize: 10}, MinSize: Size{Width: 790, Height: 560}, DefaultButton: &ok, CancelButton: &cancel, Layout: VBox{Margins: Margins{Left: 20, Top: 16, Right: 20, Bottom: 16}, Spacing: 12}, Children: []Widget{
		Label{Text: "Выберите период для названий файлов и данные для сверки."},
		GroupBox{Title: "Период реестров", Layout: VBox{Margins: Margins{Left: 16, Top: 12, Right: 16, Bottom: 14}, Spacing: 12}, Children: []Widget{
			Composite{Layout: HBox{Spacing: 10}, Children: []Widget{HSpacer{}, PushButton{Text: "‹", ToolTipText: "Предыдущий год", MinSize: Size{Width: 42, Height: 34}, OnClicked: func() { year.SetValue(max(1900, year.Value()-1)) }}, NumberEdit{AssignTo: &year, MinSize: Size{Width: 150, Height: 34}, MaxSize: Size{Width: 150}, Font: Font{Family: "Segoe UI", PointSize: 13, Bold: true}, MinValue: 1900, MaxValue: 9999, Value: float64(now.Year()), Decimals: 0, SpinButtonsVisible: true, OnValueChanged: refreshPeriod}, PushButton{Text: "›", ToolTipText: "Следующий год", MinSize: Size{Width: 42, Height: 34}, OnClicked: func() { year.SetValue(min(9999, year.Value()+1)) }}, HSpacer{}}},
			Composite{Layout: Grid{Columns: 4, Spacing: 10}, Children: months},
			Label{AssignTo: &selectedPeriod, Text: "", Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}},
		}},
		Label{Text: "Исходный файл с извещениями:"},
		Composite{Layout: HBox{}, Children: []Widget{LineEdit{AssignTo: &source, ReadOnly: true, StretchFactor: 1}, PushButton{Text: "Выбрать файл…", OnClicked: func() {
			f := walk.FileDialog{Title: "Исходный файл", Filter: "Книга Excel (*.xlsx)|*.xlsx", InitialDirPath: platform.RegistrySourceRoot}
			if accepted, e := f.ShowOpen(dlg); e != nil {
				walk.MsgBox(dlg, "Выбор файла", e.Error(), walk.MsgBoxIconError)
			} else if accepted {
				source.SetText(f.FilePath)
				validate()
			}
		}}}},
		Label{Text: "Папка с выгрузкой ОВЗИД:"},
		Composite{Layout: HBox{}, Children: []Widget{LineEdit{AssignTo: &exports, ReadOnly: true, StretchFactor: 1}, PushButton{Text: "Выбрать папку…", OnClicked: func() {
			downloads, e := platform.Downloads()
			if e != nil {
				walk.MsgBox(dlg, "Выбор папки", e.Error(), walk.MsgBoxIconError)
				return
			}
			initialDir := filepath.Join(downloads, "Выгрузка ОВЗИД для реестров ФССП")
			folder, accepted, e := platform.ChooseFolder(uintptr(dlg.Handle()), "Папка с выгрузкой ОВЗИД", initialDir)
			if e != nil {
				walk.MsgBox(dlg, "Выбор папки", e.Error(), walk.MsgBoxIconError)
			} else if accepted {
				exports.SetText(folder)
				validate()
			}
		}}}},
		Label{Text: "Результат: новая подпапка «Сверка» рядом с исходным файлом. Прежние комплекты сохраняются."},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "ОК", Enabled: false, OnClicked: func() {
			result = registry.Options{Source: source.Text(), Exports: exports.Text(), Month: month, Year: int(year.Value())}
			dlg.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}},
	}}).Create(owner)
	if err != nil {
		return result, false, err
	}
	defer dlg.Dispose()
	refreshPeriod()
	code := dlg.Run()
	return result, code == walk.DlgCmdOK, nil
}

func (a *App) startRegisters() {
	if a.running {
		if a.register != nil && a.register.running {
			a.showOperation(a.register)
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
	opt, accepted, err := chooseRegisters(a.window)
	if err != nil {
		a.error(err)
		return
	}
	if !accepted {
		return
	}
	op, err := newOperation(a, "Создание реестров возбуждения и окончания", true, func() {
		if a.registerOutput != "" {
			if e := platform.OpenFolder(a.registerOutput); e != nil {
				a.error(e)
			}
		}
	}, true)
	if err != nil {
		a.error(err)
		return
	}
	a.register = op
	a.registerOutput = ""
	op.running = true
	a.running = true
	ctx, cancel := context.WithCancel(a.lifecycle)
	a.cancel = cancel
	op.cancel = cancel
	op.stop.SetEnabled(true)
	a.status.SetText("Создание реестров выполняется…")
	a.enable()
	go func() {
		defer cancel()
		result, runErr := registry.Run(ctx, opt, func(e registry.Event) {
			a.window.Synchronize(func() {
				op.model.stages[e.Stage] = e
				op.model.PublishRowChanged(e.Stage)
				op.status.SetText(e.Detail)
				progress := 0
				if e.Stage == 0 && e.Total > 0 {
					progress = e.Checked * 60 / e.Total
				} else if e.Stage > 0 {
					steps := len(registry.StageNames) - 1
					progress = 60 + (e.Stage-1)*40/steps
					if e.State == "Сохранён" {
						progress = 60 + e.Stage*40/steps
					}
				}
				op.progress.SetValue(max(op.progress.Value(), progress))
			})
		})
		a.window.Synchronize(func() {
			op.running = false
			a.running = false
			a.cancel = nil
			op.cancel = nil
			op.stop.SetEnabled(false)
			if runErr != nil {
				for i, e := range op.model.stages {
					if e.State == "Ожидание" || e.State == "Подготовлен" {
						op.model.stages[i].State = "Отменён"
						op.model.stages[i].Detail = "Комплект не сохранён"
					}
				}
				op.model.PublishRowsReset()
				message := "Создание реестров не выполнено"
				if errors.Is(runErr, context.Canceled) {
					message = "Создание реестров остановлено"
				} else if !a.closing {
					a.error(runErr)
				}
				op.status.SetText(message)
				a.status.SetText(message)
				a.notify(message, "Новый комплект не сохранён. Прежние результаты не изменены.")
			} else {
				a.registerOutput = result.Dir
				op.progress.SetValue(100)
				message := fmt.Sprintf("Сохранено файлов: %d. Проверено %d/%d. Д.р. по ИД: %d, по делу: %d. Без д.р. по ИД: %d.", len(result.Files), result.Total, result.Total, result.Found, result.FoundByCase, result.Total-result.Found)
				op.status.SetText(message)
				a.status.SetText("Реестры созданы")
				a.notify("Реестры созданы", message)
			}
			a.enable()
			if a.closing {
				a.window.Close()
			}
		})
	}()
}
