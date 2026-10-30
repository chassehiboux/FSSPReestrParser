package platform

import (
	"golang.org/x/sys/windows/registry"
	"os"
)

// RememberExecutable позволяет Excel-загрузчику находить приложение после запуска из любой папки.
func RememberExecutable() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\FSSPReestrParser`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue("ExecutablePath", path)
}
