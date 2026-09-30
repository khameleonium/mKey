// Package logfile — файл журнала с ротацией по размеру (NFR-8).
//
// Когда файл превышает MaxBytes, он переименовывается в <имя>.1, прежний .1 — в .2 и так
// далее до Backups; самый старый удаляется. Так журнал не растёт бесконечно.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Writer — io.Writer в файл с ротацией. Безопасен для одновременной записи из нескольких горутин.
type Writer struct {
	// path — путь к текущему файлу журнала.
	path string
	// maxBytes — размер, после которого файл ротируется.
	maxBytes int64
	// backups — сколько старых файлов хранить.
	backups int

	// mu защищает файл и счётчик размера.
	mu   sync.Mutex
	file *os.File
	size int64
}

// Open открывает (или создаёт) файл журнала с правами 0600, создавая каталог 0700.
func Open(path string, maxBytes int64, backups int) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w := &Writer{path: path, maxBytes: maxBytes, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// Write дописывает p в журнал, при необходимости сначала ротируя файл.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Ротация, если запись превысит предел (пустой файл не ротируем, даже если запись большая).
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	// Запись.
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close закрывает файл журнала.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// open открывает текущий файл на дозапись и запоминает его размер.
func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file, w.size = f, fi.Size()
	return nil
}

// rotate сдвигает старые файлы (.1 → .2 …), переименовывает текущий в .1 и открывает новый.
func (w *Writer) rotate() error {
	// Закрываем текущий файл.
	if err := w.file.Close(); err != nil {
		return err
	}

	// Сдвигаем старые файлы, самый старый перезаписывается.
	for i := w.backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	if w.backups > 0 {
		if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Новый пустой файл.
	return w.open()
}
