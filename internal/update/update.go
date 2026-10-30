package update

import (
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const Repository = "chassehiboux/FSSPReestrParser"
const AssetName = "FSSPReestrParser-windows-amd64.exe"

var Version = "0.1.0-dev"

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}
type Release struct {
	Version    string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}
type cache struct {
	Checked time.Time `json:"checked"`
	Release Release   `json:"release"`
}

// Контекст вызывающего кода задаёт отдельные сроки проверки версии и скачивания пакета.
var client = &http.Client{}

func request(ctx context.Context, link string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", link, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "FSSPReestrParser/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	return client.Do(req)
}
func Check(ctx context.Context, local string) (*Release, error) {
	var c cache
	raw, _ := os.ReadFile(filepath.Join(local, "release-cache.json"))
	json.Unmarshal(raw, &c)
	response, e := request(ctx, "https://api.github.com/repos/"+Repository+"/releases/latest")
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return nil, nil
	}
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Проверка обновления: HTTP %d", response.StatusCode)
	}
	var r Release
	if e = json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&r); e != nil {
		return nil, e
	}
	if r.Draft || r.Prerelease {
		return nil, nil
	}
	if _, e = parseVersion(r.Version); e != nil {
		return nil, e
	}
	raw, _ = json.Marshal(cache{Checked: time.Now(), Release: r})
	os.WriteFile(filepath.Join(local, "release-cache.json"), raw, 0600)
	if Newer(r.Version, Version) {
		return &r, nil
	}
	return nil, nil
}
func parseVersion(s string) ([3]int, error) {
	var out [3]int
	s = strings.TrimPrefix(s, "v")
	s = strings.SplitN(s, "-", 2)[0]
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, errors.New("Некорректная версия выпуска")
	}
	for i, p := range parts {
		n, e := strconv.Atoi(p)
		if e != nil || n < 0 {
			return out, errors.New("Некорректная версия выпуска")
		}
		out[i] = n
	}
	return out, nil
}
func Newer(remote, current string) bool {
	a, e := parseVersion(remote)
	if e != nil {
		return false
	}
	b, e := parseVersion(current)
	if e != nil {
		return false
	}
	for i := range a {
		if a[i] > b[i] {
			return true
		}
		if a[i] < b[i] {
			return false
		}
	}
	return strings.Contains(current, "-dev") && !strings.Contains(remote, "-")
}
func assetURL(r Release, name string) (string, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			u, e := url.Parse(a.URL)
			prefix := "https://github.com/" + Repository + "/releases/download/"
			if e != nil || u.Scheme != "https" || !strings.HasPrefix(a.URL, prefix) {
				return "", errors.New("Некорректный адрес файла обновления")
			}
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("В выпуске отсутствует %s", name)
}
func Download(ctx context.Context, r Release, local string, progress func(int64, int64)) (string, error) {
	link, e := assetURL(r, AssetName)
	if e != nil {
		return "", e
	}
	checksumURL, e := assetURL(r, "checksums.txt")
	if e != nil {
		return "", e
	}
	res, e := request(ctx, checksumURL)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("Контрольная сумма: HTTP %d", res.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if e != nil {
		return "", e
	}
	expected := ""
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == AssetName {
			expected = strings.ToLower(fields[0])
		}
	}
	if len(expected) != 64 {
		return "", errors.New("Не найдена контрольная сумма приложения")
	}
	folder, e := os.MkdirTemp(local, "update-*")
	if e != nil {
		return "", e
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(folder)
		}
	}()
	response, e := request(ctx, link)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("Обновление: HTTP %d", response.StatusCode)
	}
	path := filepath.Join(folder, "FSSPReestrParser.exe")
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		return "", e
	}
	hash := sha256.New()
	var done int64
	buf := make([]byte, 128*1024)
	for {
		n, readErr := response.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > 256*1024*1024 {
				file.Close()
				return "", errors.New("Слишком большой пакет обновления")
			}
			if _, e = file.Write(buf[:n]); e != nil {
				file.Close()
				return "", e
			}
			hash.Write(buf[:n])
			progress(done, response.ContentLength)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return "", readErr
		}
	}
	if e = file.Close(); e != nil {
		return "", e
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return "", errors.New("Контрольная сумма обновления не совпала")
	}
	image, e := pe.Open(path)
	if e != nil {
		return "", errors.New("Обновление не является Windows-приложением")
	}
	machine := image.FileHeader.Machine
	image.Close()
	if machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return "", errors.New("Обновление предназначено для другой архитектуры")
	}
	ok = true
	return path, nil
}
func LaunchHelper(candidate, target string) error {
	cmd := exec.Command(candidate, "--apply-update", "--target", target, "--parent-pid", strconv.Itoa(os.Getpid()))
	return cmd.Start()
}

// Apply выполняется скачанным EXE после подтверждения обновления пользователем.
func Apply(target string, parent uint32) (resultErr error) {
	unlock, lockErr := updateLock(target)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	self, e := os.Executable()
	if e != nil {
		return e
	}
	if !filepath.IsAbs(target) || strings.EqualFold(self, target) {
		return errors.New("Некорректный путь установки")
	}
	if proc, e := windows.OpenProcess(windows.SYNCHRONIZE, false, parent); e == nil {
		state, err := windows.WaitForSingleObject(proc, 60000)
		windows.CloseHandle(proc)
		if err != nil || state == uint32(windows.WAIT_TIMEOUT) {
			return errors.New("Приложение не закрылось для обновления")
		}
	}
	defer func() {
		if resultErr != nil {
			if info, err := os.Stat(target); err == nil && !info.IsDir() {
				exec.Command(target, "--skip-update-once").Start()
			}
		}
	}()
	next := target + ".next"
	backup := target + ".previous"
	ready := filepath.Join(filepath.Dir(self), "ready")
	os.Remove(ready)
	os.Remove(next)
	in, e := os.Open(self)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(next, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	os.Remove(backup)
	if e = os.Rename(target, backup); e != nil {
		return e
	}
	if e = os.Rename(next, target); e != nil {
		os.Rename(backup, target)
		return e
	}
	cmd := exec.Command(target, "--update-ready", ready, "--update-helper-pid", strconv.Itoa(os.Getpid()))
	if e = cmd.Start(); e != nil {
		return rollback(target, backup, e)
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if _, e = os.Stat(ready); e == nil {
			os.Remove(backup)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	return rollback(target, backup, errors.New("Новая версия не подтвердила запуск"))
}
func rollback(target, backup string, cause error) error {
	if e := os.Remove(target); e != nil {
		return fmt.Errorf("%v; восстановление: %w", cause, e)
	}
	if e := os.Rename(backup, target); e != nil {
		return fmt.Errorf("%v; восстановление: %w", cause, e)
	}
	return fmt.Errorf("Восстановлена предыдущая версия: %w", cause)
}
