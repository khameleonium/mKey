package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/buildinfo"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml и префикс i18n-ключей.
const ModuleID = "update"

// Config — настройки модуля из секции modules.update в config.yaml.
type Config struct {
	// Check — раз в сутки проверять, вышла ли новая версия (по умолчанию нет: mKey не ходит в
	// сеть без согласия человека).
	Check bool `json:"check"`
	// Repo — репозиторий выпусков на GitHub; API — адрес API GitHub (другой — для зеркала).
	Repo string `json:"repo"`
	API  string `json:"api"`
}

// Тайминги и пределы.
const (
	// checkEvery — как часто проверять при включённой проверке; firstCheck — первая проверка
	// после запуска (не мешать запуску).
	checkEvery = 24 * time.Hour
	firstCheck = time.Minute
	// httpTimeout — предел одного запроса; maxDownload — предел размера архива.
	httpTimeout = 2 * time.Minute
	maxDownload = 200 << 20
)

// Module — реализация contracts.Module для модуля «update».
type Module struct {
	// log, bus, tr — из Init; cfg — настройки; notifier, life — сервисы (могут быть nil).
	log      *slog.Logger
	bus      contracts.Bus
	tr       contracts.Translator
	cfg      Config
	notifier contracts.Notifier
	life     contracts.Lifecycle

	// apiBase — адрес API GitHub (подменяется в тестах); client — HTTP-клиент; exe — путь
	// установленной программы; home — домашняя папка; version и gui — эта сборка.
	apiBase string
	client  *http.Client
	exe     string
	home    string
	version string
	gui     bool

	// mu защищает info, notified и cfg.Check.
	mu       sync.Mutex
	info     contracts.UpdateInfo
	notified string

	// Фоновая проверка.
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New создаёт модуль. Зависимости модуль получает в Init, а не в конструкторе.
func New() *Module {
	return &Module{cfg: Config{Repo: "khameleonium/mKey", API: "https://api.github.com"},
		client: &http.Client{Timeout: httpTimeout}, version: buildinfo.Version, gui: buildinfo.GUI}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки и публикует сервис обновлений.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	m.log, m.bus, m.tr = host.Logger(), host.Bus(), host.I18n()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if m.cfg.Repo == "" || m.cfg.API == "" {
		return fmt.Errorf("%s: repo and api must not be empty", ModuleID)
	}
	m.apiBase = strings.TrimSuffix(m.cfg.API, "/")
	m.notifier, _ = contracts.LookupService[contracts.Notifier](host.Services())
	m.life, _ = contracts.LookupService[contracts.Lifecycle](host.Services())

	// Программа, которую заменять: запущенный файл (настоящий путь, без ссылок).
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		m.exe = exe
	}
	m.home = os.Getenv("HOME")
	m.info = m.base()
	return contracts.ProvideService[contracts.Updater](host.Services(), m)
}

// Start запускает проверку раз в сутки (если она включена — проверяется перед каждой попыткой).
func (m *Module) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Go(func() { m.loop(ctx) })
	return nil
}

// Stop останавливает фоновую проверку.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	return nil
}

// loop проверяет обновления через минуту после запуска и затем раз в сутки, пока проверка включена.
func (m *Module) loop(ctx context.Context) {
	wait := firstCheck
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = checkEvery
		m.mu.Lock()
		on := m.cfg.Check
		m.mu.Unlock()
		if !on {
			continue
		}
		info, err := m.Check(ctx)
		if err != nil {
			m.log.Warn("update check failed", "err", err)
			continue
		}
		m.announce(info)
	}
}

// announce сообщает о новой версии один раз: уведомление и событие шины.
func (m *Module) announce(info contracts.UpdateInfo) {
	m.mu.Lock()
	seen := m.notified == info.Latest
	m.notified = info.Latest
	m.mu.Unlock()
	if !info.Available || seen {
		return
	}
	m.bus.Publish(contracts.TopicUpdateAvailable, info)
	if m.notifier != nil {
		body := m.tr.T("update.available", contracts.Arg{Name: "version", Value: info.Latest})
		_ = m.notifier.Notify(context.Background(), "mKey", body)
	}
}

// base — сведения без проверки: текущая версия и может ли mKey обновиться сам.
func (m *Module) base() contracts.UpdateInfo {
	info := contracts.UpdateInfo{Current: m.version, Check: m.cfg.Check, CanApply: true}
	if _, ok := buildinfo.CompareVersions(m.version, "0.0.0"); !ok {
		info.CanApply, info.Reason = false, contracts.UpdateDev
	} else if !m.inHome() {
		info.CanApply, info.Reason = false, contracts.UpdatePackage
	}
	return info
}

// inHome сообщает, что программа лежит в домашней папке (установлена мастером, не пакетом).
func (m *Module) inHome() bool {
	if m.exe == "" || m.home == "" {
		return false
	}
	rel, err := filepath.Rel(m.home, m.exe)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Info возвращает сведения по последней проверке (contracts.Updater).
func (m *Module) Info() contracts.UpdateInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	info := m.info
	info.Check = m.cfg.Check
	return info
}

// SetCheck включает или выключает проверку раз в сутки (contracts.Updater).
func (m *Module) SetCheck(on bool) {
	m.mu.Lock()
	m.cfg.Check = on
	m.mu.Unlock()
}

// release — выпуск на GitHub: тег, страница и файлы.
type release struct {
	Tag    string `json:"tag_name"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// errNoReleases — выпусков ещё нет (GitHub отвечает 404 на «последний выпуск»).
var errNoReleases = errors.New("no releases yet")

// latest спрашивает последний выпуск; выпусков нет — errNoReleases.
func (m *Module) latest(ctx context.Context) (release, error) {
	var r release
	url := m.apiBase + "/repos/" + m.cfg.Repo + "/releases/latest"
	body, err := m.get(ctx, url, 1<<20)
	if errors.Is(err, errNotFound) {
		return r, errNoReleases
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return r, fmt.Errorf("release info: %w", err)
	}
	return r, nil
}

// errNotFound — адрес не найден (404).
var errNotFound = errors.New("not found")

// get скачивает адрес целиком (не больше limit байт).
func (m *Module) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mkey/"+m.version)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", url, errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", url, limit)
	}
	return data, nil
}

// Check спрашивает последний выпуск (contracts.Updater).
func (m *Module) Check(ctx context.Context) (contracts.UpdateInfo, error) {
	// Выпусков ещё нет — это не ошибка: новых версий просто нет.
	r, err := m.latest(ctx)
	if errors.Is(err, errNoReleases) {
		info := m.base()
		m.mu.Lock()
		m.info = info
		info.Check = m.cfg.Check
		m.mu.Unlock()
		return info, nil
	}
	if err != nil {
		return m.Info(), err
	}
	info := m.base()
	info.Latest, info.URL = strings.TrimPrefix(r.Tag, "v"), r.URL
	if c, ok := buildinfo.CompareVersions(info.Latest, m.version); ok && c > 0 {
		info.Available = true
	}
	m.mu.Lock()
	m.info = info
	info.Check = m.cfg.Check
	m.mu.Unlock()
	return info, nil
}

// assetName — архив своей сборки: полная или консольная, своя архитектура.
func (m *Module) assetName(version string) (archive, binary string) {
	binary = "mkey"
	if !m.gui {
		binary = "mkey-cli"
	}
	return fmt.Sprintf("%s_%s_linux_%s.tar.gz", binary, version, runtime.GOARCH), binary
}

// Apply скачивает, проверяет и устанавливает новую версию, затем перезапускает mKey
// (contracts.Updater).
func (m *Module) Apply(ctx context.Context) (contracts.UpdateInfo, error) {
	// Можно ли и есть ли что ставить.
	info, err := m.Check(ctx)
	if err != nil {
		return info, err
	}
	if !info.CanApply {
		return info, fmt.Errorf("%w: %s", contracts.ErrCannotUpdate, info.Reason)
	}
	if !info.Available {
		return info, errors.New("no newer version")
	}
	r, err := m.latest(ctx)
	if err != nil {
		return info, err
	}

	// Архив и контрольные суммы выпуска.
	archive, binary := m.assetName(info.Latest)
	urls := map[string]string{}
	for _, a := range r.Assets {
		urls[a.Name] = a.URL
	}
	if urls[archive] == "" || urls["checksums.txt"] == "" {
		return info, fmt.Errorf("release %s has no %s or checksums.txt", info.Latest, archive)
	}
	sums, err := m.get(ctx, urls["checksums.txt"], 1<<20)
	if err != nil {
		return info, err
	}
	want := checksum(sums, archive)
	if want == "" {
		return info, fmt.Errorf("checksums.txt has no %s", archive)
	}
	data, err := m.get(ctx, urls[archive], maxDownload)
	if err != nil {
		return info, err
	}

	// Сверка суммы: не совпала — ничего не меняем.
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return info, fmt.Errorf("checksum mismatch for %s: got %s, want %s", archive, got, want)
	}

	// Программа из архива — рядом с установленной, затем замена переименованием
	// (прежняя версия остаётся как mkey.old).
	bin, err := extract(data, binary)
	if err != nil {
		return info, err
	}
	if err := replace(m.exe, bin); err != nil {
		return info, err
	}
	m.log.Info("mkey updated", "from", m.version, "to", info.Latest, "exe", m.exe)

	// Перезапуск новой программой (после ответа вызывающему).
	if m.life != nil {
		exe := m.exe
		go func() {
			time.Sleep(500 * time.Millisecond)
			m.life.Restart(exe)
		}()
	}
	return info, nil
}

// checksum находит сумму файла name в checksums.txt («<sha256>  <имя>» в строке).
func checksum(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// extract достаёт из архива .tar.gz файл name (в корне архива).
func extract(data []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive has no %s", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Clean(h.Name) == name {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// replace атомарно заменяет программу path новой: запись во временный файл рядом, старая версия
// — в path.old, новая — переименованием на место path.
func replace(path string, bin []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	_ = os.Remove(path + ".old")
	if err := os.Link(path, path+".old"); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("keep previous version: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Проверки на этапе компиляции.
var (
	_ contracts.Module  = (*Module)(nil)
	_ contracts.Updater = (*Module)(nil)
)
