package contracts

import (
	"context"
	"errors"
	"time"
)

// Запись и воспроизведение ввода (модуль recorder, этап 6, FR-REC-1…4).

// Темы шины записи и воспроизведения.
const (
	// TopicRecordingStarted — запись началась; Payload: RecordingInfo.
	TopicRecordingStarted = "recorder.started"
	// TopicRecordingStopped — запись закончилась и сохранена; Payload: RecordingInfo.
	TopicRecordingStopped = "recorder.stopped"
)

// ErrNotRecording — запись сейчас не идёт.
var ErrNotRecording = errors.New("not recording")

// ErrAlreadyRecording — запись уже идёт.
var ErrAlreadyRecording = errors.New("already recording")

// ErrRecordingNotFound — записи с таким именем нет.
var ErrRecordingNotFound = errors.New("recording not found")

// RecordOptions — параметры новой записи.
type RecordOptions struct {
	// Name — имя записи (файл <имя>.mkrec в каталоге записей); пусто — mKeyRec_ДДММГГГГ_ЧЧММСС.
	Name string `json:"name"`
	// Kinds — классы записываемых устройств ("keyboard", "mouse", "gamepad"…); пусто — из настроек записи.
	Kinds []string `json:"kinds,omitempty"`
}

// RecordSettings — настройки записи по умолчанию (секция modules.recorder в config.yaml): с ними
// запись начинается без вопросов — сочетанием, из меню значка, из окна и командой mkey rec.
type RecordSettings struct {
	// Kinds — какие устройства записывать ("keyboard", "mouse", "touchpad", "gamepad"…).
	Kinds []string `json:"kinds"`
	// Moves — записывать движения мыши (false — только нажатия кнопок и колесо).
	Moves bool `json:"moves"`
	// MergeMovesMS — движения мыши ближе этого (мс) склеиваются в одно при записи: файл короче,
	// путь курсора тот же (0 — записывать каждое движение).
	MergeMovesMS int `json:"merge_moves_ms"`
	// CenterPointer — ставить курсор в центр экрана перед записью (запись движений начинается
	// от известной точки и повторяется точнее).
	CenterPointer bool `json:"center_pointer"`
	// CoalesceMS — движения мыши ближе этого (мс) склеиваются при воспроизведении (0 — нет).
	CoalesceMS int `json:"coalesce_ms"`
}

// RecordingInfo — сведения о записи.
type RecordingInfo struct {
	// Name — имя записи; Path — путь к файлу.
	Name string `json:"name"`
	Path string `json:"path"`
	// Created — когда начата запись.
	Created time.Time `json:"created"`
	// DurationMS — длительность в миллисекундах (для идущей записи — сколько записано).
	DurationMS int64 `json:"duration_ms"`
	// Events — число записанных событий (без служебных).
	Events int `json:"events"`
	// Devices — имена записанных устройств.
	Devices []string `json:"devices,omitempty"`
	// StopHotkey — сочетание, которым можно закончить идущую запись из любой программы ("" — нет).
	StopHotkey string `json:"stop_hotkey,omitempty"`
	// Problem — файл не читается (ошибка после ручной правки или запись первой версии); nil — всё в порядке.
	Problem *RecordingProblem `json:"problem,omitempty"`
}

// RecordingProblem — ошибка в файле записи: где она и что не так.
type RecordingProblem struct {
	// Line — номер строки с 1 (0 — файл целиком); Text — сама строка, как в файле.
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
	// Code — вид ошибки (mkrec.Problem*: key, action, old_format…; read — файл не прочитан);
	// Arg — неверное слово, клавиша или номер.
	Code string `json:"code"`
	Arg  string `json:"arg,omitempty"`
	// Message — понятное сообщение на языке клиента (заполняет API).
	Message string `json:"message,omitempty"`
}

// Recorder — запись ввода с физических устройств (модуль recorder).
type Recorder interface {
	// StartRecording начинает запись. ErrAlreadyRecording — запись уже идёт.
	StartRecording(opts RecordOptions) (RecordingInfo, error)
	// StopRecording заканчивает запись и сохраняет файл. cutChord — сочетание, которым её
	// остановили ("^{Ctrl}{C}" в терминале): если оно только что было нажато, оно вырезается из записи.
	// ErrNotRecording — запись не идёт.
	StopRecording(cutChord string) (RecordingInfo, error)
	// Recording возвращает сведения об идущей записи; false — запись не идёт.
	Recording() (RecordingInfo, bool)
	// WaitRecording ждёт окончания идущей записи (кнопкой, сочетанием или командой) и возвращает её.
	WaitRecording(ctx context.Context) (RecordingInfo, error)
	// Recordings возвращает сохранённые записи (новые — первыми).
	Recordings() ([]RecordingInfo, error)
	// DeleteRecording удаляет запись. ErrRecordingNotFound — такой нет.
	DeleteRecording(name string) error
	// RecordHotkey возвращает сочетание «начать/закончить запись» ("" — выключено).
	RecordHotkey() string
	// SetRecordHotkey меняет сочетание записи сразу ("" — выключить).
	SetRecordHotkey(combo string) error
	// RecordSettings возвращает настройки записи по умолчанию.
	RecordSettings() RecordSettings
	// SetRecordSettings проверяет и меняет настройки записи (действуют со следующей записи).
	// ErrBadRecordSettings — неизвестный класс устройств или значение вне допустимого.
	SetRecordSettings(s RecordSettings) error
	// ConvertRecording превращает запись в блоки конструктора (FR-REC-6): создаёт выключенный
	// проект с одним событием (запуск вручную) и возвращает его ID. ErrRecordingNotFound — нет записи;
	// ErrRecordingEmpty — в записи нет действий, которые можно превратить.
	ConvertRecording(name string, opts ConvertOptions) (string, error)
}

// ConvertOptions — параметры превращения записи в блоки.
type ConvertOptions struct {
	// Simplify — упрощать движения мыши: меньше блоков, путь почти тот же.
	Simplify bool `json:"simplify"`
}

// ErrRecordingEmpty — в записи нет действий клавиатуры и мыши, которые можно превратить в блоки.
var ErrRecordingEmpty = errors.New("recording has no convertible actions")

// ErrBadRecordSettings — настройки записи неверны (неизвестный класс устройств, значение вне допустимого).
var ErrBadRecordSettings = errors.New("invalid recording settings")

// PlayOptions — параметры воспроизведения.
type PlayOptions struct {
	// Speed — скорость (1 — как записано, 2 — вдвое быстрее); допустимо 0.1–10, 0 — 1.
	Speed float64 `json:"speed"`
	// Repeat — сколько раз повторить (0 — 1 раз; -1 — пока не остановят).
	Repeat int `json:"repeat"`
	// SkipMoves — не повторять перемещения мыши (только кнопки и клавиши).
	SkipMoves bool `json:"skip_moves"`
}

// Player — воспроизведение записей через виртуальные устройства mKey (модуль recorder).
type Player interface {
	// Play воспроизводит запись name и ждёт окончания; отмена ctx останавливает воспроизведение
	// и отпускает все нажатые им клавиши. ErrRecordingNotFound — такой записи нет.
	Play(ctx context.Context, name string, opts PlayOptions) error
	// StopPlayback останавливает все воспроизведения и возвращает, сколько их было.
	StopPlayback() int
	// Playing возвращает, сколько воспроизведений идёт сейчас.
	Playing() int
}
