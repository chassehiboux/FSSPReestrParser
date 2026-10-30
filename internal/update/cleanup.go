package update

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func updateLock(target string) (func(), error) {
	path, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(path)))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Local\FSSPUpdate-%x`, sum))
	// Mutex Windows принадлежит потоку, а не горутине.
	runtime.LockOSThread()
	locked := false
	defer func() {
		if !locked {
			runtime.UnlockOSThread()
		}
	}()
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return nil, err
	}
	status, err := windows.WaitForSingleObject(h, 0)
	if err != nil || status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("установка обновления уже выполняется")
	}
	locked = true
	return func() { windows.ReleaseMutex(h); windows.CloseHandle(h); runtime.UnlockOSThread() }, nil
}

// Удаление только прямой служебной подпапки и только известных файлов, без рекурсии.
func cleanFolder(root, folder string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	folder, err = filepath.Abs(folder)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(folder), root) || !strings.HasPrefix(filepath.Base(folder), "update-") {
		return fmt.Errorf("некорректная папка обновления")
	}
	info, err := os.Lstat(folder)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("папка обновления является ссылкой или файлом")
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 || e.Name() != "FSSPReestrParser.exe" && e.Name() != "ready" {
			return fmt.Errorf("в папке есть посторонние файлы")
		}
	}
	// Работающий EXE Windows удалить не позволит; в этом случае не трогаем и ready.
	if err = os.Remove(filepath.Join(folder, "FSSPReestrParser.exe")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.Remove(filepath.Join(folder, "ready")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(folder)
}

func Cleanup(local, target string) {
	if unlock, err := updateLock(target); err == nil {
		os.Remove(target + ".previous")
		os.Remove(target + ".next")
		unlock()
	}
	entries, _ := os.ReadDir(local)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "update-") {
			continue
		}
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 10*time.Minute {
			cleanFolder(local, filepath.Join(local, e.Name()))
		}
	}
}

// Запускается новой версией: ждёт выхода установщика, чей EXE нельзя удалить из него самого.
func CleanupAfterHelper(local, target, folder string, pid uint32) {
	if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid); err == nil {
		status, err := windows.WaitForSingleObject(h, 90000)
		windows.CloseHandle(h)
		if err != nil || status == uint32(windows.WAIT_TIMEOUT) {
			return
		}
	}
	for attempt := 0; attempt < 20; attempt++ {
		if cleanFolder(local, folder) == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	Cleanup(local, target)
}
