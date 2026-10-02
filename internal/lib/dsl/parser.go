package dsl

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"mkey/internal/lib/devmap"
	"mkey/internal/lib/keys"
)

// Ограничения, защищающие от «вечных» и гигантских макросов (SEC-4).
const (
	// MaxRepeat — наибольшее число повторов нажатия или группы.
	MaxRepeat = 10000
	// MaxDurationMS — наибольшая длительность паузы или удержания (1 час).
	MaxDurationMS = 3_600_000
)

// BuiltinCommands — встроенные команды языка. Плагины добавляют свои через Options.Commands.
var BuiltinCommands = []string{"Move", "Click", "Wheel", "Touch", "Swipe"}

// Options — настройки разбора.
type Options struct {
	// Commands — дополнительные команды (канонические имена), кроме встроенных.
	Commands []string
	// ExtraKey вызывается для имени без префикса устройства, которого нет в таблице клавиш
	// (например, кнопка устройства с авто-ID {UnKey001}). Возвращает каноническое имя и true, если имя известно.
	ExtraKey func(name string) (string, bool)
}

// Parse разбирает текст макроса с настройками по умолчанию.
func Parse(src string) ([]Node, error) {
	return ParseWith(src, Options{})
}

// ParseWith разбирает текст макроса. Ошибка всегда имеет тип *Error.
func ParseWith(src string, opts Options) ([]Node, error) {
	// Индекс команд без учёта регистра: строчное имя → каноническое.
	cmds := map[string]string{}
	for _, c := range append(append([]string{}, BuiltinCommands...), opts.Commands...) {
		cmds[strings.ToLower(c)] = c
	}

	// Разбираем весь текст как последовательность верхнего уровня.
	p := &parser{src: []rune(src), line: 1, col: 1, cmds: cmds, extraKey: opts.ExtraKey}
	nodes, err := p.sequence(false, Pos{})
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

// parser — состояние разбора: текст в виде рун и текущая позиция.
type parser struct {
	src       []rune
	i         int
	line, col int
	cmds      map[string]string
	extraKey  func(string) (string, bool)
}

// prefix — префикс действия перед "{".
type prefix int

const (
	prefixNone prefix = iota
	prefixDown        // ^
	prefixUp          // ~
)

// pos возвращает текущую позицию.
func (p *parser) pos() Pos { return Pos{Line: p.line, Col: p.col} }

// eof сообщает, что текст закончился.
func (p *parser) eof() bool { return p.i >= len(p.src) }

// peek возвращает текущий символ (0 в конце текста).
func (p *parser) peek() rune {
	if p.eof() {
		return 0
	}
	return p.src[p.i]
}

// peekNext возвращает символ, следующий за текущим (0 за концом текста).
func (p *parser) peekNext() rune {
	if p.i+1 >= len(p.src) {
		return 0
	}
	return p.src[p.i+1]
}

// next возвращает текущий символ и сдвигается на следующий, считая строки и столбцы.
func (p *parser) next() rune {
	r := p.src[p.i]
	p.i++
	if r == '\n' {
		p.line++
		p.col = 1
	} else {
		p.col++
	}
	return r
}

// skipSpace пропускает пробельные символы и комментарии "// …".
func (p *parser) skipSpace() {
	for !p.eof() {
		switch r := p.peek(); {
		case unicode.IsSpace(r):
			p.next()
		case r == '/' && p.peekNext() == '/':
			for !p.eof() && p.peek() != '\n' {
				p.next()
			}
		default:
			return
		}
	}
}

// unexpected возвращает ошибку для текущего символа или для неожиданного конца текста.
func (p *parser) unexpected(endCode string, endPos Pos) *Error {
	if p.eof() {
		return newError(endPos, endCode)
	}
	return newError(p.pos(), ErrUnexpectedChar, "char", string(p.peek()))
}

// sequence разбирает последовательность действий до конца текста или до ")" (внутри группы).
func (p *parser) sequence(inGroup bool, groupStart Pos) ([]Node, error) {
	var nodes []Node
	for {
		// Пропускаем пробелы; конец текста допустим только вне группы.
		p.skipSpace()
		if p.eof() {
			if inGroup {
				return nil, newError(groupStart, ErrUnclosedGroup)
			}
			return nodes, nil
		}

		// Выбираем вид элемента по первому символу.
		var (
			n   Node
			err error
		)
		switch r := p.peek(); {
		case r == '^' || r == '~' || r == '{':
			n, err = p.action()
		case r == '[':
			n, err = p.pause()
		case r == '(':
			n, err = p.group()
		case r == ')' && inGroup:
			return nodes, nil
		case r == ')' || r == '}' || r == ']':
			return nil, newError(p.pos(), ErrUnexpectedClose, "char", string(r))
		case r == '"' || unicode.IsLetter(r) || unicode.IsDigit(r):
			return nil, newError(p.pos(), ErrTextOutside, "char", string(r))
		default:
			return nil, newError(p.pos(), ErrUnexpectedChar, "char", string(r))
		}
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
}

// action разбирает действие в фигурных скобках с необязательным префиксом ^ или ~.
func (p *parser) action() (Node, error) {
	start := p.pos()

	// Префикс «зажать» или «отпустить».
	pre := prefixNone
	switch p.peek() {
	case '^':
		pre = prefixDown
		p.next()
	case '~':
		pre = prefixUp
		p.next()
	}
	if p.peek() != '{' {
		return Node{}, p.unexpected(ErrUnexpectedEnd, p.pos())
	}
	p.next()
	p.skipSpace()

	// Выбираем содержимое по первому символу.
	switch r := p.peek(); {
	case p.eof():
		return Node{}, newError(start, ErrUnclosedBrace)
	case r == '{' || (r == '}' && p.peekNext() == '}'):
		// Считаются только крайние скобки: {{} и {}} — «клавиша» с именем «{» или «}».
		// Такой клавиши нет (это Shift и [ или ]) — объясняем, как быть.
		p.next()
		return Node{}, newError(start, ErrBraceKey, "char", string(r))
	case r == '}':
		return Node{}, newError(start, ErrEmptyBraces)
	case r == '"':
		return p.textAction(start, pre)
	case r == '*':
		return p.releaseAll(start, pre)
	}
	return p.keyOrCommand(start, pre)
}

// closeBrace пропускает пробелы и ожидает "}".
func (p *parser) closeBrace(start Pos) error {
	p.skipSpace()
	if p.peek() != '}' {
		return p.unexpected(ErrUnclosedBrace, start)
	}
	p.next()
	return nil
}

// textAction разбирает {"текст"}.
func (p *parser) textAction(start Pos, pre prefix) (Node, error) {
	// Перед текстом не бывает ^ и ~.
	if pre != prefixNone {
		return Node{}, newError(start, ErrPrefixNotAllowed)
	}
	text, err := p.jsonString()
	if err != nil {
		return Node{}, err
	}
	if err := p.closeBrace(start); err != nil {
		return Node{}, err
	}
	return Node{Kind: KindText, Pos: start, Text: text}, nil
}

// releaseAll разбирает ~{*}.
func (p *parser) releaseAll(start Pos, pre prefix) (Node, error) {
	p.next()
	if pre != prefixUp {
		return Node{}, newError(start, ErrStarOnlyRelease)
	}
	if err := p.closeBrace(start); err != nil {
		return Node{}, err
	}
	return Node{Kind: KindUpAll, Pos: start}, nil
}

// keyOrCommand разбирает команду ({Move +10 -5}) или запись с одной клавишей ({A}, {A*3}, {Space 500}).
// В скобках — всегда одна клавиша (решение владельца): {Ctrl+C} — ошибка с подсказкой, как записать
// сочетание зажатием: ^{Ctrl}{C}~{Ctrl}.
func (p *parser) keyOrCommand(start Pos, pre prefix) (Node, error) {
	// Первая ссылка на клавишу; если это имя команды без префикса устройства — разбираем команду.
	refPos := p.pos()
	ref, err := p.keyRef()
	if err != nil {
		return Node{}, err
	}
	if ref.Device == "" && ref.Code == nil {
		if canon, ok := p.cmds[strings.ToLower(ref.Name)]; ok && (unicode.IsSpace(p.peek()) || p.peek() == '}') {
			if pre != prefixNone {
				return Node{}, newError(start, ErrPrefixNotAllowed)
			}
			return p.command(start, canon)
		}
	}

	// Проверяем имя; «+» после клавиши — попытка записать сочетание в одних скобках.
	if err := p.checkKey(&ref, refPos); err != nil {
		return Node{}, err
	}
	n := Node{Kind: KindTap, Pos: start, Keys: []KeyRef{ref}}
	p.skipSpace()
	if p.peek() == '+' {
		return Node{}, p.chordError(start, ref)
	}

	// Необязательный повтор "*N". Повтор 1 хранится как 0 («без повтора»), чтобы дерево было каноническим.
	hasRepeat := false
	if p.peek() == '*' {
		p.next()
		p.skipSpace()
		if n.Repeat, err = p.repeatCount(); err != nil {
			return Node{}, err
		}
		hasRepeat = true
		p.skipSpace()
	}

	// Необязательное удержание (число после пробела).
	if isDigit(p.peek()) {
		if hasRepeat {
			return Node{}, newError(p.pos(), ErrRepeatAndHold)
		}
		if n.HoldMS, err = p.duration(); err != nil {
			return Node{}, err
		}
		p.skipSpace()
	}

	// Необязательное значение оси "=v" — только для одной клавиши без повтора и удержания.
	if p.peek() == '=' {
		p.next()
		p.skipSpace()
		if len(n.Keys) > 1 || hasRepeat || n.HoldMS > 0 {
			return Node{}, newError(start, ErrAxisChord)
		}
		if n.Value, err = p.signedNumber(); err != nil {
			return Node{}, err
		}
		n.Kind = KindAxis
	}
	if err := p.closeBrace(start); err != nil {
		return Node{}, err
	}

	// Префикс превращает нажатие в «зажать» или «отпустить»; повтор, удержание и ось с ним несовместимы.
	if pre != prefixNone {
		if n.Kind == KindAxis || hasRepeat || n.HoldMS > 0 {
			return Node{}, newError(start, ErrPrefixNotAllowed)
		}
		n.Kind = KindDown
		if pre == prefixUp {
			n.Kind = KindUp
		}
	}
	return n, nil
}

// chordError дочитывает запись вида {Ctrl+Alt+C} и возвращает ошибку с подсказкой, как записать
// её по правилам: зажатием (^{Ctrl}^{Alt}{C}~{Alt}~{Ctrl} в макросе, ^{Ctrl}^{Alt}{C} в горячей клавише).
func (p *parser) chordError(start Pos, first KeyRef) error {
	names := []string{formatKeyRef(first)}
	for p.peek() == '+' {
		p.next()
		p.skipSpace()
		ref, err := p.keyRef()
		if err != nil {
			break
		}
		_ = p.checkKey(&ref, p.pos())
		names = append(names, formatKeyRef(ref))
		p.skipSpace()
	}

	// Подсказки: зажать все, кроме последней, нажать последнюю, отпустить в обратном порядке.
	var macro, hotkey, release strings.Builder
	for i, n := range names {
		if i == len(names)-1 {
			macro.WriteString("{" + n + "}")
			hotkey.WriteString("{" + n + "}")
			continue
		}
		macro.WriteString("^{" + n + "}")
		hotkey.WriteString("^{" + n + "}")
		release.WriteString("~{" + names[len(names)-2-i] + "}")
	}
	macro.WriteString(release.String())
	return newError(start, ErrChord, "keys", strings.Join(names, "+"), "macro", macro.String(), "hotkey", hotkey.String())
}

// keyRef разбирает ссылку на клавишу: "Name", "device.Name", "device.001" или "#30" / "#0x110".
func (p *parser) keyRef() (KeyRef, error) {
	// Сырой код evdev.
	if p.peek() == '#' {
		start := p.pos()
		p.next()
		word := p.word()
		code, err := strconv.ParseUint(word, 0, 16)
		if err != nil || word == "" {
			return KeyRef{}, newError(start, ErrBadNumber, "text", "#"+word)
		}
		c := uint16(code)
		return KeyRef{Code: &c}, nil
	}

	// Имя, возможно с префиксом устройства.
	if !isWordStart(p.peek()) {
		return KeyRef{}, p.unexpected(ErrUnclosedBrace, p.pos())
	}
	first := p.word()
	if p.peek() != '.' || !isWordChar(p.peekNext()) {
		return KeyRef{Name: first}, nil
	}
	p.next()
	return KeyRef{Device: first, Name: p.word()}, nil
}

// checkKey проверяет имя клавиши устройства по умолчанию и заменяет его каноническим.
// Имена с префиксом устройства и сырые коды проверяются позже, при компиляции.
func (p *parser) checkKey(ref *KeyRef, pos Pos) error {
	// Ссылки на устройства и сырые коды здесь не проверяются.
	if ref.Device != "" || ref.Code != nil {
		return nil
	}

	// Слитная запись кнопки авто-ID-устройства: {UnKey001} → устройство UnKey, кнопка 001 (FR-DEV-2).
	if dev, btn, ok := devmap.SplitJoined(ref.Name); ok {
		ref.Device, ref.Name = dev, btn
		return nil
	}

	// Имя из таблицы клавиш mKey.
	if k, ok := keys.Lookup(ref.Name); ok {
		ref.Name = k.Name
		return nil
	}

	// Дополнительные имена (кнопки устройств с авто-ID и именами, данными человеком).
	if p.extraKey != nil {
		if canon, ok := p.extraKey(ref.Name); ok {
			ref.Name = canon
			return nil
		}
	}

	// Неизвестное имя: подсказываем ближайшее, если оно есть.
	if s := keys.Suggest(ref.Name); s != "" {
		return newError(pos, ErrUnknownKeyHint, "name", ref.Name, "suggestion", s)
	}
	return newError(pos, ErrUnknownKey, "name", ref.Name)
}

// command разбирает аргументы команды до "}".
func (p *parser) command(start Pos, name string) (Node, error) {
	n := Node{Kind: KindCommand, Pos: start, Command: name}
	for {
		// Конец команды.
		p.skipSpace()
		if p.eof() {
			return Node{}, newError(start, ErrUnclosedBrace)
		}
		if p.peek() == '}' {
			p.next()
			return n, nil
		}

		// Аргумент: число (возможно, со знаком) или слово.
		switch r := p.peek(); {
		case r == '+' || r == '-' || isDigit(r):
			signed := r == '+' || r == '-'
			v, err := p.signedNumber()
			if err != nil {
				return Node{}, err
			}
			// Число может быть процентом экрана: {Touch 50% 80%}.
			percent := p.peek() == '%'
			if percent {
				p.next()
			}
			n.Args = append(n.Args, Arg{Number: v, Signed: signed, Percent: percent})
		case isWordStart(r):
			n.Args = append(n.Args, Arg{Word: p.word()})
		default:
			return Node{}, newError(p.pos(), ErrUnexpectedChar, "char", string(r))
		}
	}
}

// pause разбирает паузу "[250]", "[1.5s]" или "[100..300]".
func (p *parser) pause() (Node, error) {
	start := p.pos()
	p.next()
	p.skipSpace()

	// Первая (или единственная) длительность.
	minMS, err := p.duration()
	if err != nil {
		return Node{}, err
	}
	maxMS := minMS
	p.skipSpace()

	// Необязательная верхняя граница случайной паузы.
	if p.peek() == '.' && p.peekNext() == '.' {
		p.next()
		p.next()
		p.skipSpace()
		if maxMS, err = p.duration(); err != nil {
			return Node{}, err
		}
		if maxMS < minMS {
			return Node{}, newError(start, ErrPauseRange)
		}
		p.skipSpace()
	}

	// Закрывающая скобка.
	if p.peek() != ']' {
		return Node{}, p.unexpected(ErrUnclosedPause, start)
	}
	p.next()
	return Node{Kind: KindPause, Pos: start, MinMS: minMS, MaxMS: maxMS}, nil
}

// group разбирает группу "( … )" с необязательным повтором "*N".
func (p *parser) group() (Node, error) {
	start := p.pos()
	p.next()

	// Содержимое группы до ")".
	children, err := p.sequence(true, start)
	if err != nil {
		return Node{}, err
	}
	p.next()
	n := Node{Kind: KindGroup, Pos: start, Children: children}

	// Необязательный повтор сразу после ")" (допускаются пробелы).
	save := *p
	p.skipSpace()
	if p.peek() != '*' {
		*p = save
		return n, nil
	}
	p.next()
	p.skipSpace()
	if n.Repeat, err = p.repeatCount(); err != nil {
		return Node{}, err
	}
	return n, nil
}

// repeatCount разбирает число повторов (1..MaxRepeat).
func (p *parser) repeatCount() (int, error) {
	start := p.pos()
	word := p.digits()
	n, err := strconv.Atoi(word)
	if err != nil {
		return 0, newError(start, ErrBadNumber, "text", word)
	}
	if n < 1 || n > MaxRepeat {
		return 0, newError(start, ErrBadRepeat, "max", strconv.Itoa(MaxRepeat))
	}
	if n == 1 {
		return 0, nil
	}
	return n, nil
}

// duration разбирает длительность "250", "250ms", "1.5s" и возвращает миллисекунды.
func (p *parser) duration() (int64, error) {
	start := p.pos()

	// Число.
	num := p.number()
	if num == "" {
		return 0, p.unexpected(ErrUnexpectedEnd, start)
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, newError(start, ErrBadDuration, "text", num)
	}

	// Единицы: ms (по умолчанию) или s. После единиц не должно быть букв ("5sec" — ошибка).
	unit := p.word()
	switch strings.ToLower(unit) {
	case "", "ms":
	case "s":
		v *= 1000
	default:
		return 0, newError(start, ErrBadDuration, "text", num+unit)
	}

	// Ограничение длительности проверяется до перевода в целое, чтобы огромные числа не переполнились.
	if v > MaxDurationMS {
		return 0, newError(start, ErrTooLong, "max", "1h")
	}
	return int64(math.Round(v)), nil
}

// signedNumber разбирает число с необязательным знаком: "+10", "-5", "0.5".
func (p *parser) signedNumber() (float64, error) {
	start := p.pos()
	sign := 1.0
	switch p.peek() {
	case '+':
		p.next()
	case '-':
		sign = -1
		p.next()
	}
	num := p.number()
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, newError(start, ErrBadNumber, "text", num)
	}
	return sign * v, nil
}

// number читает число вида "123" или "1.5" (без знака).
func (p *parser) number() string {
	var b strings.Builder
	b.WriteString(p.digits())
	if p.peek() == '.' && isDigit(p.peekNext()) {
		b.WriteRune(p.next())
		b.WriteString(p.digits())
	}
	return b.String()
}

// digits читает последовательность цифр.
func (p *parser) digits() string {
	var b strings.Builder
	for isDigit(p.peek()) {
		b.WriteRune(p.next())
	}
	return b.String()
}

// word читает слово: буквы любого алфавита, цифры и "_" (так же читается и код вида 0x110).
func (p *parser) word() string {
	var b strings.Builder
	for isWordChar(p.peek()) {
		b.WriteRune(p.next())
	}
	return b.String()
}

// jsonString разбирает строку в кавычках с экранированиями JSON (docs/dsl.md, «Набор текста»).
// В отличие от JSON, внутри строки допускаются настоящие переводы строк, а \f запрещён.
func (p *parser) jsonString() (string, error) {
	start := p.pos()
	p.next() // открывающая кавычка

	var b strings.Builder
	for {
		// Конец текста без закрывающей кавычки.
		if p.eof() {
			return "", newError(start, ErrUnclosedText)
		}
		r := p.next()

		// Закрывающая кавычка или обычный символ.
		if r == '"' {
			return b.String(), nil
		}
		if r != '\\' {
			b.WriteRune(r)
			continue
		}

		// Экранирование.
		escPos := Pos{Line: p.line, Col: p.col - 1}
		if p.eof() {
			return "", newError(start, ErrUnclosedText)
		}
		switch e := p.next(); e {
		case '"', '\\', '/':
			b.WriteRune(e)
		case 'n':
			b.WriteRune('\n')
		case 'r':
			b.WriteRune('\r')
		case 't':
			b.WriteRune('\t')
		case 'b':
			b.WriteRune('\b')
		case 'u':
			r, err := p.unicodeEscape(escPos)
			if err != nil {
				return "", err
			}
			b.WriteRune(r)
		default:
			return "", newError(escPos, ErrBadEscape, "escape", `\`+string(e))
		}
	}
}

// unicodeEscape разбирает \uXXXX (после "\u"), включая суррогатную пару 😀.
func (p *parser) unicodeEscape(pos Pos) (rune, error) {
	// Первые четыре шестнадцатеричные цифры.
	r1, ok := p.hex4()
	if !ok {
		return 0, newError(pos, ErrBadEscape, "escape", `\u`)
	}
	if !utf16.IsSurrogate(r1) {
		return r1, nil
	}

	// Суррогатная пара: сразу должна идти вторая половина.
	if p.peek() != '\\' || p.peekNext() != 'u' {
		return 0, newError(pos, ErrBadEscape, "escape", `\u`)
	}
	p.next()
	p.next()
	r2, ok := p.hex4()
	r := utf16.DecodeRune(r1, r2)
	if !ok || r == unicode.ReplacementChar {
		return 0, newError(pos, ErrBadEscape, "escape", `\u`)
	}
	return r, nil
}

// hex4 читает ровно четыре шестнадцатеричные цифры.
func (p *parser) hex4() (rune, bool) {
	if p.i+4 > len(p.src) {
		return 0, false
	}
	v, err := strconv.ParseUint(string(p.src[p.i:p.i+4]), 16, 32)
	if err != nil {
		return 0, false
	}
	for range 4 {
		p.next()
	}
	return rune(v), true
}

// isDigit сообщает, является ли символ цифрой ASCII.
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// isWordStart сообщает, может ли символ начинать слово (буква любого алфавита, цифра или "_").
func isWordStart(r rune) bool { return isWordChar(r) }

// isWordChar сообщает, может ли символ входить в слово.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || isDigit(r) || r == '_'
}
