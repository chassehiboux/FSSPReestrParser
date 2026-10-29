package main

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"

	"github.com/chassehiboux/FSSPReestrParser/internal/platform"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"github.com/chassehiboux/FSSPReestrParser/internal/ui"
	"github.com/chassehiboux/FSSPReestrParser/internal/update"
	"github.com/lxn/walk"
)

var SharedSettings string

func main() {
	runtime.LockOSThread()
	apply := flag.Bool("apply-update", false, "")
	target := flag.String("target", "", "")
	parent := flag.Uint("parent-pid", 0, "")
	ready := flag.String("update-ready", "", "")
	skip := flag.Bool("skip-update-once", false, "")
	flag.Parse()
	if *apply {
		if err := update.Apply(*target, uint32(*parent)); err != nil {
			walk.MsgBox(nil, "Обновление не установлено", err.Error(), walk.MsgBoxIconError)
		}
		return
	}
	local, err := platform.LocalDir()
	if err != nil {
		walk.MsgBox(nil, "Ошибка", err.Error(), walk.MsgBoxIconError)
		return
	}
	shared := SharedSettings
	if env := os.Getenv("FSSP_SETTINGS_PATH"); env != "" {
		shared = env
	}
	if shared == "" {
		walk.MsgBox(nil, "Настройки не подключены", "Эта сборка не подключена к общим настройкам. Обратитесь к администратору приложения.", walk.MsgBoxIconError)
		return
	}
	if err = ui.Run(&settings.Store{Shared: shared, Local: local}, local, *ready, *skip); err != nil {
		os.WriteFile(filepath.Join(local, "startup-error.txt"), []byte(err.Error()), 0600)
		walk.MsgBox(nil, "Ошибка запуска", err.Error(), walk.MsgBoxIconError)
	}
}
