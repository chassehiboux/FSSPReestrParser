package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cleanupFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("тестовые данные, не исполняемый файл"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupRemovesOnlyStaleKnownFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "app.exe")
	cleanupFile(t, target)
	cleanupFile(t, target+".previous")
	cleanupFile(t, target+".next")
	for _, name := range []string{"update-old", "update-young", "update-custom", "pipelines"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		cleanupFile(t, filepath.Join(dir, "FSSPReestrParser.exe"))
		if name == "update-custom" {
			cleanupFile(t, filepath.Join(dir, "important.txt"))
		}
		if name != "update-young" {
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(dir, past, past); err != nil {
				t.Fatal(err)
			}
		}
	}
	Cleanup(root, target)
	for _, path := range []string{target + ".previous", target + ".next", filepath.Join(root, "update-old")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("остаток не удалён", path, err)
		}
	}
	for _, path := range []string{target, filepath.Join(root, "update-young"), filepath.Join(root, "update-custom", "important.txt"), filepath.Join(root, "pipelines")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("затронут посторонний или свежий файл", path, err)
		}
	}
}

func TestCleanupDoesNotRemoveBackupDuringInstallation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "app.exe")
	cleanupFile(t, target+".previous")
	unlock, err := updateLock(target)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { Cleanup(root, target); close(done) }()
	<-done
	_, err = os.Stat(target + ".previous")
	unlock()
	if err != nil {
		t.Fatal("резерв удалён во время установки", err)
	}
	Cleanup(root, target)
	if _, err = os.Stat(target + ".previous"); !os.IsNotExist(err) {
		t.Fatal("резерв остался после завершения", err)
	}
}

func TestCompletedFolderCleanupAndScopeGuard(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "update-completed")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cleanupFile(t, filepath.Join(dir, "FSSPReestrParser.exe"))
	cleanupFile(t, filepath.Join(dir, "ready"))
	CleanupAfterHelper(root, filepath.Join(root, "app.exe"), dir, 0)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("папка завершённого установщика осталась", err)
	}
	outside := t.TempDir()
	cleanupFile(t, filepath.Join(outside, "ready"))
	if cleanFolder(root, outside) == nil {
		t.Fatal("принята чужая папка")
	}
	if _, err := os.Stat(filepath.Join(outside, "ready")); err != nil {
		t.Fatal(err)
	}
}
