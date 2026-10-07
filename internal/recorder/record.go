package recorder

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/dsl"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/keys"
	"github.com/khameleonium/mKey/internal/lib/mkrec"
)

// keepWindow — сколько последних событий держится в памяти до записи в файл: столько можно
// вырезать из конца записи (сочетание остановки нажимают быстрее).
const keepWindow = 2 * time.Second

// cutRecent — сколько времени после нажатия сочетания остановки из терминала (Ctrl+C) его ещё
// можно вырезать: команда остановки приходит чуть позже самого нажатия.
const cutRecent = 3 * time.Second

// nameCleanRe — символы, недопустимые в имени записи (имя — это имя файла).
var nameCleanRe = regexp.MustCompile(`[^\p{L}\p{N} _\-.()]`)

// downKey — клавиша устройства записи: для отслеживания нажатий.
type downKey struct {
	dev  int
	code uint16
}

// session — идущая запись.
type session struct {
	// info — сведения о записи; start — время начала (по часам событий ядра).
	info  contracts.RecordingInfo
	start time.Time
	// kinds — какие классы устройств записываются.
	kinds map[string]bool
	// moves — записывать движения мыши; merge — окно их склейки (0 — не склеивать).
	moves bool
	merge time.Duration
	// header — сведения для заголовка (устройства дописываются по мере появления).
	header mkrec.Header
	// draft и w — черновик действий на диске (без заголовка); path — итоговый файл.
	draft *os.File
	w     *mkrec.Writer
	path  string
	// ids — номера устройств записи по путям (-1 — устройство не записывается).
	ids map[string]int
	// open — незавершённые (до SYN_REPORT) действия по устройствам.
	open map[int]*mkrec.Frame
	// buf — последние действия, ещё не записанные в черновик; written — сколько уже записано.
	buf     []mkrec.Frame
	written int
	// down — нажатые в записи клавиши (отпускания без нажатия не записываются);
	// quiet — номер действия, после которого не было нажатых клавиш (для вырезания при экстренной остановке).
	down  map[downKey]bool
	quiet int
	// cuts — сочетания остановки из терминала, которые можно вырезать ("^{Ctrl}{C}").
	cuts map[string]*chord
	// done закрывается по окончании записи; result и err — её итог.
	done   chan struct{}
	result contracts.RecordingInfo
	err    error
}

// total возвращает число действий записи (записанных и в памяти).
func (s *session) total() int { return s.written + len(s.buf) }

// add добавляет событие устройства в запись.
// devices возвращает текущие устройства ввода (вызывается, только если устройство ещё не встречалось).
func (s *session) add(e contracts.InputEvent, devices func() map[string]contracts.InputDevice) {
	// Сочетания остановки из терминала отслеживаются по всем событиям.
	for _, c := range s.cuts {
		c.feed(e.Event, s.total(), e.Event.Time)
	}

	// Номер устройства: при первой встрече решаем, записывать ли его.
	id, ok := s.ids[e.Device]
	if !ok {
		id = s.register(e.Device, devices()[e.Device])
	}
	if id < 0 {
		return
	}

	// Конец пакета устройства — действие готово.
	if e.Event.IsSync() {
		s.closeFrame(id)
		return
	}

	// Записывается то, что получили программы: «съеденное» mKey пропускаем, переназначенное —
	// как переназначено. Время — от начала записи; события до начала пропускаются.
	d := e.Delivered
	if e.Consumed || !mkrec.Keep(d) {
		return
	}
	if d.Type != ev.EvKey && d.Type != ev.EvRel && d.Type != ev.EvAbs {
		return
	}
	t := e.Event.Time.Sub(s.start)
	if t < 0 {
		return
	}

	// Движения мыши не записываются, если так задано в настройках (колесо записывается).
	if !s.moves && d.Type == ev.EvRel && (d.Code == ev.RelX || d.Code == ev.RelY) {
		return
	}

	// Отпускание клавиши, нажатой до начала записи, не записывается.
	if d.Type == ev.EvKey {
		k := downKey{id, d.Code}
		switch d.Value {
		case ev.ValueDown:
			s.down[k] = true
		case ev.ValueUp:
			if !s.down[k] {
				return
			}
			delete(s.down, k)
		}
	}

	// В открытое действие устройства.
	f := s.open[id]
	if f == nil {
		f = &mkrec.Frame{T: t, Device: id}
		s.open[id] = f
	}
	f.Events = append(f.Events, ev.Event{Type: d.Type, Code: d.Code, Value: d.Value})
}

// closeFrame завершает открытое действие устройства: склеивает перемещения мыши, кладёт
// в память и сбрасывает старые действия в черновик.
func (s *session) closeFrame(id int) {
	f := s.open[id]
	delete(s.open, id)
	if f == nil || len(f.Events) == 0 {
		return
	}

	// Перемещение мыши склеивается с последним перемещением того же устройства, если оно недавнее.
	if f.IsMove() {
		for i := len(s.buf) - 1; i >= 0; i-- {
			last := &s.buf[i]
			if last.Device != id {
				continue
			}
			if last.IsMove() && f.T-last.T < s.merge {
				last.Events = mkrec.AddMoves(last.Events, f.Events)
				return
			}
			break
		}
	}

	// Действие — в память; после него нет нажатых клавиш — запоминаем «тихое» место.
	s.buf = append(s.buf, *f)
	if len(s.down) == 0 {
		s.quiet = s.total()
	}
	s.flush(f.T - keepWindow)
}

// register решает, записывать ли устройство (по его классам), и выдаёт ему номер (-1 — нет).
func (s *session) register(path string, d contracts.InputDevice) int {
	match := false
	kinds := make([]string, len(d.Kinds))
	for i, k := range d.Kinds {
		kinds[i] = string(k)
		match = match || s.kinds[string(k)]
	}
	if !match {
		s.ids[path] = -1
		return -1
	}
	id := len(s.header.Devices)
	s.header.Devices = append(s.header.Devices, mkrec.Device{ID: id, Kinds: kinds, Name: d.Info.Name, Caps: capsOf(d)})
	s.ids[path] = id
	return id
}

// capsOf возвращает возможности устройства для его копии при воспроизведении (T12.2): у клавиатур
// и мышей копия не нужна (их повторяют клавиатура и мышь mKey) — nil.
func capsOf(d contracts.InputDevice) *mkrec.Caps {
	plain := len(d.Kinds) > 0
	for _, k := range d.Kinds {
		plain = plain && (k == ev.KindKeyboard || k == ev.KindMouse)
	}
	if plain {
		return nil
	}
	c := d.Info.Caps
	return &mkrec.Caps{
		ID: d.Info.ID, Props: slices.Clone(c.Props), Keys: slices.Clone(c.Codes[ev.EvKey]),
		Rels: slices.Clone(c.Codes[ev.EvRel]), Abs: maps.Clone(c.Abs),
	}
}

// flush пишет в черновик действия раньше before.
func (s *session) flush(before time.Duration) {
	n := 0
	for n < len(s.buf) && s.buf[n].T < before {
		if err := s.w.WriteFrame(s.buf[n]); err != nil && s.err == nil {
			s.err = err
		}
		n++
	}
	s.written += n
	s.buf = s.buf[n:]
}

// truncate убирает из конца записи действия начиная с номера idx (если они ещё в памяти)
// и возвращает время первого убранного (0 — ничего не убрано).
func (s *session) truncate(idx int) time.Duration {
	i := idx - s.written
	if i < 0 || i >= len(s.buf) {
		return 0
	}
	t := s.buf[i].T
	s.buf = s.buf[:i]
	return t
}

// StartRecording начинает запись (contracts.Recorder).
func (m *Module) StartRecording(opts contracts.RecordOptions) (contracts.RecordingInfo, error) {
	if m.input == nil {
		return contracts.RecordingInfo{}, fmt.Errorf("recording: %w", errNoInput)
	}

	// Настройки записи — снимок на момент начала (их могут поменять во время записи).
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()

	// Имя записи: заданное (проверенное) или mKeyRec_ДДММГГГГ_ЧЧММСС.
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultName(m.now())
	}
	if !validName(name) {
		return contracts.RecordingInfo{}, fmt.Errorf("%w: %q", errBadName, name)
	}

	// Калибровка мыши: указатель — в центр экрана, запись движений начинается от известной точки
	// (FR-REC-5; работает в любом окружении). До блокировки: устройство может готовиться полсекунды.
	centered := cfg.CenterPointer && m.centerPointer()

	// Черновик действий рядом с итоговым файлом.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sess != nil {
		return contracts.RecordingInfo{}, contracts.ErrAlreadyRecording
	}
	if err := os.MkdirAll(m.cfg.Dir, 0o700); err != nil {
		return contracts.RecordingInfo{}, err
	}
	path := filepath.Join(m.cfg.Dir, name+mkrec.FileExt)
	draft, err := os.CreateTemp(m.cfg.Dir, "."+name+".*.part")
	if err != nil {
		return contracts.RecordingInfo{}, err
	}

	// Сессия: классы устройств, сочетания для вырезания, время начала.
	kinds := opts.Kinds
	if len(kinds) == 0 {
		kinds = cfg.Kinds
	}
	s := &session{
		start: m.now(), draft: draft, w: mkrec.NewWriter(draft), path: path,
		moves: cfg.Moves, merge: time.Duration(cfg.MergeMovesMS) * time.Millisecond,
		kinds: map[string]bool{}, ids: map[string]int{}, open: map[int]*mkrec.Frame{},
		down: map[downKey]bool{}, cuts: map[string]*chord{}, done: make(chan struct{}),
		header: mkrec.Header{Centered: centered},
	}
	s.header.Created = s.start
	for _, k := range kinds {
		s.kinds[k] = true
	}
	if c, err := parseChord(CutCtrlC); err == nil {
		s.cuts[CutCtrlC] = c
	}
	s.info = contracts.RecordingInfo{Name: name, Path: path, Created: s.start, StopHotkey: m.cfg.Hotkey}
	m.sess = s
	m.log.Info("recording started", "name", name, "pointer_centered", centered)
	m.bus.Publish(contracts.TopicRecordingStarted, s.info)
	return s.info, nil
}

// centerPointer ставит указатель мыши в центр рабочего стола; false — не удалось (нет вывода).
func (m *Module) centerPointer() bool {
	if m.devs == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), centerTimeout)
	defer cancel()
	if err := m.devs.CenterPointer(ctx); err != nil {
		m.log.Warn("pointer not centered", "err", err)
		return false
	}
	return true
}

// centerTimeout — сколько ждать постановки указателя (первый раз устройство готовится ~0,5 с).
const centerTimeout = 2 * time.Second

// StopRecording заканчивает запись (contracts.Recorder).
func (m *Module) StopRecording(cutChord string) (contracts.RecordingInfo, error) {
	m.mu.Lock()
	s := m.sess
	var cut *chord
	if s != nil {
		cut = s.cuts[cutChord]
	}
	m.mu.Unlock()
	if s == nil {
		return contracts.RecordingInfo{}, contracts.ErrNotRecording
	}
	return m.stop(s, cutAt(cut, m.now()))
}

// cutAt возвращает номер действия, с которого вырезать сочетание c, если его только что нажали
// (-1 — не вырезать).
func cutAt(c *chord, now time.Time) int {
	if c == nil || c.completedAt.IsZero() || now.Sub(c.completedAt) >= cutRecent {
		return -1
	}
	return c.first
}

// stop заканчивает запись s: вырезает конец записи с действия cut (-1 — ничего не вырезать),
// дописывает черновик и собирает итоговый файл с заголовком.
func (m *Module) stop(s *session, cut int) (contracts.RecordingInfo, error) {
	m.mu.Lock()
	if m.sess != s {
		m.mu.Unlock()
		return contracts.RecordingInfo{}, contracts.ErrNotRecording
	}
	m.sess = nil
	now := m.now()

	// Вырезаем сочетание остановки и считаем длительность; незавершённые действия дописываем.
	duration := now.Sub(s.start)
	for id := range s.open {
		s.closeFrame(id)
	}
	if cut >= 0 {
		if t := s.truncate(cut); t > 0 {
			duration = t
		}
	}
	s.flush(time.Duration(1<<62 - 1))
	m.mu.Unlock()

	// Итоговый файл: заголовок, действия из черновика, длительность.
	info, err := s.finish(duration)
	s.result, s.err = info, err
	close(s.done)
	if err != nil {
		return info, err
	}
	m.log.Info("recording saved", "name", info.Name, "actions", info.Events, "duration_ms", info.DurationMS)
	m.bus.Publish(contracts.TopicRecordingStopped, info)
	return info, nil
}

// finish собирает итоговый файл записи из черновика и удаляет черновик.
func (s *session) finish(duration time.Duration) (contracts.RecordingInfo, error) {
	draftPath := s.draft.Name()
	defer func() { _ = os.Remove(draftPath) }()

	// Черновик на диск и в начало.
	if err := errors.Join(s.err, s.w.Flush()); err != nil {
		_ = s.draft.Close()
		return s.info, err
	}
	if _, err := s.draft.Seek(0, 0); err != nil {
		_ = s.draft.Close()
		return s.info, err
	}
	defer func() { _ = s.draft.Close() }()

	// Итоговый файл пишется во временный и переименовывается — прежняя запись с тем же
	// именем заменяется только целиком готовой новой.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".final.*.part")
	if err != nil {
		return s.info, err
	}
	w := mkrec.NewWriter(tmp)
	werr := errors.Join(w.WriteHeader(s.header), w.Append(s.draft), w.Close(duration), tmp.Close())
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return s.info, werr
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		_ = os.Remove(tmp.Name())
		return s.info, err
	}

	// Сведения о готовой записи.
	info := s.info
	info.DurationMS = duration.Milliseconds()
	info.Events = s.written
	for _, d := range s.header.Devices {
		info.Devices = append(info.Devices, d.Name)
	}
	return info, nil
}

// Recording возвращает сведения об идущей записи (contracts.Recorder).
func (m *Module) Recording() (contracts.RecordingInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sess == nil {
		return contracts.RecordingInfo{}, false
	}
	info := m.sess.info
	info.DurationMS = m.now().Sub(m.sess.start).Milliseconds()
	info.Events = m.sess.total()
	return info, true
}

// WaitRecording ждёт окончания идущей записи (contracts.Recorder).
func (m *Module) WaitRecording(ctx context.Context) (contracts.RecordingInfo, error) {
	m.mu.Lock()
	s := m.sess
	m.mu.Unlock()
	if s == nil {
		return contracts.RecordingInfo{}, contracts.ErrNotRecording
	}
	select {
	case <-ctx.Done():
		return contracts.RecordingInfo{}, ctx.Err()
	case <-s.done:
		return s.result, s.err
	}
}

// Recordings возвращает сохранённые записи, новые — первыми (contracts.Recorder).
func (m *Module) Recordings() ([]contracts.RecordingInfo, error) {
	entries, err := os.ReadDir(m.cfg.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []contracts.RecordingInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []contracts.RecordingInfo{}
	for _, e := range entries {
		// Только файлы записей (черновики начинаются с точки).
		name, ok := strings.CutSuffix(e.Name(), mkrec.FileExt)
		if !ok || strings.HasPrefix(name, ".") || e.IsDir() {
			continue
		}
		path := filepath.Join(m.cfg.Dir, e.Name())
		info := contracts.RecordingInfo{Name: name, Path: path}
		if rec, err := readRecording(path); err == nil {
			// Дата — из строки created; её нет (файл написан вручную) — дата изменения файла.
			info.Created = rec.Header.Created
			if fi, err := e.Info(); err == nil && info.Created.IsZero() {
				info.Created = fi.ModTime()
			}
			info.DurationMS = rec.Duration.Milliseconds()
			info.Events = len(rec.Frames)
			for _, d := range rec.Header.Devices {
				info.Devices = append(info.Devices, d.Name)
			}
		} else {
			// Файл не читается (ошибка после ручной правки, запись первой версии) — показываем
			// с датой изменения и описанием ошибки, чтобы человек знал, что и где поправить.
			if fi, err := e.Info(); err == nil {
				info.Created = fi.ModTime()
			}
			info.Problem = recordingProblem(err)
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b contracts.RecordingInfo) int { return b.Created.Compare(a.Created) })
	return out, nil
}

// recordingProblem описывает ошибку чтения файла записи для списка записей.
func recordingProblem(err error) *contracts.RecordingProblem {
	var p *mkrec.Problem
	if errors.As(err, &p) {
		return &contracts.RecordingProblem{Line: p.Line, Text: p.Text, Code: p.Code, Arg: p.Arg}
	}
	return &contracts.RecordingProblem{Code: "read", Arg: err.Error()}
}

// DeleteRecording удаляет запись (contracts.Recorder).
func (m *Module) DeleteRecording(name string) error {
	path, err := m.recordingPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	// В журнал — какая запись удалена (содержимое нажатий не пишется, NFR-8).
	m.log.Info("recording deleted", "name", name)
	return nil
}

// recordingPath возвращает путь существующей записи; ErrRecordingNotFound — нет такой.
func (m *Module) recordingPath(name string) (string, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), mkrec.FileExt)
	if !validName(name) {
		return "", fmt.Errorf("%w: %q", errBadName, name)
	}
	path := filepath.Join(m.cfg.Dir, name+mkrec.FileExt)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%w: %s", contracts.ErrRecordingNotFound, name)
	}
	return path, nil
}

// validName сообщает, годится ли имя записи как имя файла: без путей, служебных символов
// и точки в начале.
func validName(name string) bool {
	return name != "" && len(name) <= 200 && !strings.HasPrefix(name, ".") && !nameCleanRe.MatchString(name)
}

// Ошибки модуля.
var (
	// errNoInput — модуль ввода отключён или недоступен.
	errNoInput = errors.New("input devices are unavailable")
	// errNoProjects — модуль проектов отключён: превращать запись некуда.
	errNoProjects = errors.New("projects module is disabled")
	// errBadName — имя записи содержит недопустимые символы.
	errBadName = errors.New("invalid recording name (use letters, digits, spaces, _ - . ( ))")
)

// chord — отслеживание сочетания клавиш (нажатого на любых устройствах).
type chord struct {
	// keys — клавиши сочетания; held — нажатые сейчас коды из сочетания.
	keys []keys.Key
	held map[uint16]bool
	// first — номер события записи, с которого началось нажатие сочетания (для вырезания).
	first int
	// completedAt — когда сочетание было нажато целиком последний раз.
	completedAt time.Time
	// armed — сочетание сработает при следующем полном нажатии (после отпускания всех его клавиш).
	armed bool
}

// RecordHotkey возвращает сочетание «начать/закончить запись» (contracts.Recorder).
func (m *Module) RecordHotkey() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Hotkey
}

// SetRecordHotkey меняет сочетание записи сразу ("" — выключить) (contracts.Recorder).
func (m *Module) SetRecordHotkey(combo string) error {
	var c *chord
	if combo != "" {
		var err error
		if c, err = parseChord(combo); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Hotkey, m.hotkey = combo, c
	return nil
}

// CutCtrlC — сочетание Ctrl+C, которым заканчивают запись в терминале (вырезается из записи).
const CutCtrlC = "^{Ctrl}{C}"

// parseChord разбирает сочетание записью зажатием: "^{Ctrl}^{Alt}{R}".
func parseChord(s string) (*chord, error) {
	refs, err := dsl.ParseHotkey(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	c := &chord{held: map[uint16]bool{}, armed: true}
	for _, r := range refs {
		k, ok := keys.Lookup(r.Name)
		if r.Device != "" || r.Code != nil || !ok {
			return nil, fmt.Errorf("unsupported key %q in %q", r.Name, s)
		}
		c.keys = append(c.keys, k)
	}
	return c, nil
}

// feed учитывает событие; true — сочетание только что нажато целиком. idx — номер события в записи.
func (c *chord) feed(e ev.Event, idx int, now time.Time) bool {
	// Только нажатия и отпускания клавиш сочетания.
	if e.Type != ev.EvKey || e.Value == ev.ValueRepeat || !c.matchesAny(e.Code) {
		return false
	}
	if e.Value == ev.ValueUp {
		delete(c.held, e.Code)
		if len(c.held) == 0 {
			c.armed = true
		}
		return false
	}

	// Нажатие: первое из сочетания запоминает место в записи; полное — срабатывание.
	if len(c.held) == 0 {
		c.first = idx
	}
	c.held[e.Code] = true
	if c.armed && c.complete() {
		c.armed = false
		c.completedAt = now
		return true
	}
	return false
}

// matchesAny сообщает, входит ли код в сочетание.
func (c *chord) matchesAny(code uint16) bool {
	for _, k := range c.keys {
		if k.Matches(code) {
			return true
		}
	}
	return false
}

// complete сообщает, что нажаты все клавиши сочетания.
func (c *chord) complete() bool {
	for _, k := range c.keys {
		found := false
		for code := range c.held {
			if k.Matches(code) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
